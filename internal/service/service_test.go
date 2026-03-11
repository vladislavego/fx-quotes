package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"fxquotes/internal/domain"
)

// fakeRepo satisfies both QuoteRepository (for QuoteService) and
// WorkerRepository (for Worker).
type fakeRepo struct {
	lastID     uuid.UUID
	lastPrice  *decimal.Decimal
	lastStatus domain.QuoteStatus
	lastError  *string
	calls      int
	markErr    error
	done       chan struct{}

	mu           sync.Mutex
	pendingQueue []domain.PendingTask
	// stored tracks created updates for idempotency tests.
	stored map[string]*domain.QuoteUpdate
}

func (r *fakeRepo) FindOrCreatePending(_ context.Context, pair string, requestID *string) (*domain.QuoteUpdate, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if requestID != nil && r.stored != nil {
		if existing, ok := r.stored[*requestID]; ok {
			if existing.Pair != pair {
				return nil, false, domain.ErrRequestIDPairMismatch
			}
			return existing, false, nil
		}
	}

	id := uuid.New()
	u := &domain.QuoteUpdate{
		ID:        id,
		Pair:      pair,
		Status:    domain.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if requestID != nil {
		u.RequestID = requestID
		if r.stored == nil {
			r.stored = make(map[string]*domain.QuoteUpdate)
		}
		r.stored[*requestID] = u
	}
	r.pendingQueue = append(r.pendingQueue, domain.PendingTask{ID: id, Pair: pair, Attempts: 1})
	return u, true, nil
}

func (r *fakeRepo) GetUpdateByID(_ context.Context, _ uuid.UUID) (*domain.QuoteUpdate, error) {
	return nil, domain.ErrNotFound
}

func (r *fakeRepo) GetLatestByPair(_ context.Context, _ string) (*domain.LatestQuote, error) {
	return nil, domain.ErrNotFound
}

func (r *fakeRepo) MarkDone(_ context.Context, id uuid.UUID, price decimal.Decimal) error {
	r.mu.Lock()
	r.calls++
	r.lastID = id
	r.lastPrice = &price
	r.lastStatus = domain.StatusDone
	r.lastError = nil
	r.mu.Unlock()
	if r.done != nil {
		r.done <- struct{}{}
	}
	return r.markErr
}

func (r *fakeRepo) MarkFailed(_ context.Context, id uuid.UUID, errMsg string) error {
	r.mu.Lock()
	r.calls++
	r.lastID = id
	r.lastPrice = nil
	r.lastStatus = domain.StatusFailed
	r.lastError = &errMsg
	r.mu.Unlock()
	if r.done != nil {
		r.done <- struct{}{}
	}
	return r.markErr
}

func (r *fakeRepo) ClaimBatch(_ context.Context, limit int, _ time.Duration) ([]domain.PendingTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pendingQueue) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(r.pendingQueue) {
		n = len(r.pendingQueue)
	}
	batch := make([]domain.PendingTask, n)
	copy(batch, r.pendingQueue[:n])
	r.pendingQueue = r.pendingQueue[n:]
	return batch, nil
}

type fakeRateProvider struct {
	rates map[string]decimal.Decimal
	err   error
}

func (c *fakeRateProvider) GetRates(_ context.Context, _ []string) (map[string]decimal.Decimal, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.rates, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func waitDone(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for completion")
	}
}

func testWorkerConfig(retryMaxAttempts int, retryBaseDelay, pollInterval, jobTimeout time.Duration, batchSize int) WorkerConfig {
	return WorkerConfig{
		RetryMaxAttempts: retryMaxAttempts,
		RetryBaseDelay:   retryBaseDelay,
		PollInterval:     pollInterval,
		JobTimeout:       jobTimeout,
		BatchSize:        batchSize,
		MaxClaimAttempts: 5,
		StaleAfter:       time.Minute,
	}
}

// --- QuoteService tests ---

func TestRequestUpdateSuccess(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewQuoteService(repo)

	update, err := svc.RequestUpdate(context.Background(), "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if update.Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", update.Pair)
	}
	if update.Status != domain.StatusPending {
		t.Fatalf("expected status pending, got %s", update.Status)
	}
}

func TestRequestUpdateUnsupportedPair(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewQuoteService(repo)

	_, err := svc.RequestUpdate(context.Background(), "BTC/USD", nil)
	if err == nil {
		t.Fatalf("expected error for unsupported pair")
	}
}

func TestRequestUpdateWithRequestID(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewQuoteService(repo)

	reqID := "my-request-id"
	update, err := svc.RequestUpdate(context.Background(), "EUR/MXN", &reqID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if update.Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", update.Pair)
	}
}

func TestRequestUpdateRequestIDPairMismatch(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewQuoteService(repo)

	reqID := "same-id"
	_, err := svc.RequestUpdate(context.Background(), "EUR/MXN", &reqID)
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	reqID2 := "same-id"
	_, err = svc.RequestUpdate(context.Background(), "USD/EUR", &reqID2)
	if !errors.Is(err, domain.ErrRequestIDPairMismatch) {
		t.Fatalf("expected ErrRequestIDPairMismatch, got %v", err)
	}
}

func TestRequestUpdateRequestIDSamePairIdempotent(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewQuoteService(repo)

	reqID := "same-id"
	u1, err := svc.RequestUpdate(context.Background(), "EUR/MXN", &reqID)
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	reqID2 := "same-id"
	u2, err := svc.RequestUpdate(context.Background(), "EUR/MXN", &reqID2)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if u1.ID != u2.ID {
		t.Fatalf("expected same ID for idempotent call, got %s and %s", u1.ID, u2.ID)
	}
}

// --- Worker tests ---

type testError string

func (e testError) Error() string { return string(e) }

type permanentTestError struct {
	msg string
}

func (e permanentTestError) Error() string   { return e.msg }
func (e permanentTestError) Permanent() bool { return true }

type fakePermanentRateProvider struct {
	mu    sync.Mutex
	calls int
}

func (c *fakePermanentRateProvider) GetRates(_ context.Context, _ []string) (map[string]decimal.Decimal, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return nil, permanentTestError{msg: "invalid api key"}
}

type countingRateProvider struct {
	mu        sync.Mutex
	calls     int
	failUntil int
	rates     map[string]decimal.Decimal
}

func (c *countingRateProvider) GetRates(_ context.Context, _ []string) (map[string]decimal.Decimal, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls <= c.failUntil {
		return nil, testError("transient failure")
	}
	return c.rates, nil
}

func TestWorkerProcessJobSuccess(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 1)}
	fx := &fakeRateProvider{rates: map[string]decimal.Decimal{"MXN": decimal.NewFromFloat(42.123456)}}
	svc := NewQuoteService(repo)
	worker := NewWorker(repo, fx, testLogger(), testWorkerConfig(1, 0, 10*time.Millisecond, 30*time.Second, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	update, err := svc.RequestUpdate(context.Background(), "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitDone(t, repo.done)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 1 {
		t.Fatalf("expected 1 MarkDone/MarkFailed call, got %d", repo.calls)
	}
	if repo.lastID != update.ID {
		t.Fatalf("expected id %s, got %s", update.ID, repo.lastID)
	}
	if repo.lastStatus != domain.StatusDone {
		t.Fatalf("expected status done, got %s", repo.lastStatus)
	}
	if repo.lastPrice == nil || !repo.lastPrice.Equal(decimal.NewFromFloat(42.123456)) {
		t.Fatalf("unexpected price: got %v", repo.lastPrice)
	}
}

func TestWorkerProcessJobFailure(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 1)}
	permFx := &fakePermanentRateProvider{}
	svc := NewQuoteService(repo)
	worker := NewWorker(repo, permFx, testLogger(), testWorkerConfig(1, 0, 10*time.Millisecond, 30*time.Second, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	_, err := svc.RequestUpdate(context.Background(), "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitDone(t, repo.done)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 1 {
		t.Fatalf("expected 1 MarkDone/MarkFailed call, got %d", repo.calls)
	}
	if repo.lastStatus != domain.StatusFailed {
		t.Fatalf("expected status failed, got %s", repo.lastStatus)
	}
	if repo.lastError == nil {
		t.Fatalf("expected error message on failure")
	}
}

func TestWorkerProcessJobRetrySuccess(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 1)}
	fx := &countingRateProvider{failUntil: 2, rates: map[string]decimal.Decimal{"MXN": decimal.NewFromFloat(99.5)}}
	svc := NewQuoteService(repo)
	worker := NewWorker(repo, fx, testLogger(), testWorkerConfig(3, time.Millisecond, 10*time.Millisecond, 30*time.Second, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	update, err := svc.RequestUpdate(context.Background(), "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitDone(t, repo.done)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 1 {
		t.Fatalf("expected 1 MarkDone/MarkFailed call, got %d", repo.calls)
	}
	if repo.lastID != update.ID {
		t.Fatalf("expected id %s, got %s", update.ID, repo.lastID)
	}
	if repo.lastStatus != domain.StatusDone {
		t.Fatalf("expected status done after retry, got %s", repo.lastStatus)
	}
	if repo.lastPrice == nil || !repo.lastPrice.Equal(decimal.NewFromFloat(99.5)) {
		t.Fatalf("unexpected price: got %v", repo.lastPrice)
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if fx.calls != 3 {
		t.Fatalf("expected 3 GetRates calls (2 failures + 1 success), got %d", fx.calls)
	}
}

func TestWorkerProcessJobPermanentError(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 1)}
	permFx := &fakePermanentRateProvider{}
	svc := NewQuoteService(repo)
	worker := NewWorker(repo, permFx, testLogger(), testWorkerConfig(3, time.Millisecond, 10*time.Millisecond, 30*time.Second, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	_, err := svc.RequestUpdate(context.Background(), "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitDone(t, repo.done)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.lastStatus != domain.StatusFailed {
		t.Fatalf("expected status failed, got %s", repo.lastStatus)
	}

	permFx.mu.Lock()
	defer permFx.mu.Unlock()
	if permFx.calls != 1 {
		t.Fatalf("expected 1 GetRates call (no retries for permanent error), got %d", permFx.calls)
	}
}

func TestWorkerProcessBatchMultiplePairs(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 2)}
	fx := &countingRateProvider{
		rates: map[string]decimal.Decimal{
			"MXN": decimal.NewFromFloat(20.0),
			"USD": decimal.NewFromFloat(1.1),
		},
	}
	svc := NewQuoteService(repo)
	worker := NewWorker(repo, fx, testLogger(), testWorkerConfig(1, 0, 10*time.Millisecond, 30*time.Second, 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	_, err := svc.RequestUpdate(context.Background(), "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = svc.RequestUpdate(context.Background(), "USD/EUR", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitDone(t, repo.done)
	waitDone(t, repo.done)

	fx.mu.Lock()
	defer fx.mu.Unlock()
	if fx.calls != 1 {
		t.Fatalf("expected 1 GetRates call for batch, got %d", fx.calls)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 2 {
		t.Fatalf("expected 2 MarkDone/MarkFailed calls, got %d", repo.calls)
	}
}

func TestWorkerTransientErrorLeavesTasksForReclaim(t *testing.T) {
	repo := &fakeRepo{}
	fx := &fakeRateProvider{err: testError("transient failure")}
	worker := NewWorker(repo, fx, testLogger(), testWorkerConfig(1, 0, 10*time.Millisecond, 30*time.Second, 10))

	id := uuid.New()
	repo.mu.Lock()
	repo.pendingQueue = append(repo.pendingQueue, domain.PendingTask{ID: id, Pair: "EUR/MXN", Attempts: 1})
	repo.mu.Unlock()

	worker.pollOnce(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 0 {
		t.Fatalf("expected 0 MarkDone/MarkFailed calls for transient error with low attempts, got %d", repo.calls)
	}
}

func TestWorkerTransientErrorFailsExhaustedOnly(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 1)}
	fx := &fakeRateProvider{err: testError("transient failure")}
	worker := NewWorker(repo, fx, testLogger(), WorkerConfig{
		RetryMaxAttempts: 1,
		PollInterval:     10 * time.Millisecond,
		JobTimeout:       30 * time.Second,
		BatchSize:        10,
		MaxClaimAttempts: 3,
		StaleAfter:       time.Minute,
	})

	exhaustedID := uuid.New()
	normalID := uuid.New()
	repo.mu.Lock()
	repo.pendingQueue = append(repo.pendingQueue,
		domain.PendingTask{ID: exhaustedID, Pair: "EUR/MXN", Attempts: 3},
		domain.PendingTask{ID: normalID, Pair: "EUR/MXN", Attempts: 1},
	)
	repo.mu.Unlock()

	worker.pollOnce(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 1 {
		t.Fatalf("expected 1 MarkDone/MarkFailed call for exhausted task, got %d", repo.calls)
	}
	if repo.lastID != exhaustedID {
		t.Fatalf("expected exhausted id %s, got %s", exhaustedID, repo.lastID)
	}
	if repo.lastStatus != domain.StatusFailed {
		t.Fatalf("expected status failed, got %s", repo.lastStatus)
	}
}

func TestWorkerSuccessIgnoresExhaustedAttempts(t *testing.T) {
	repo := &fakeRepo{done: make(chan struct{}, 1)}
	fx := &fakeRateProvider{rates: map[string]decimal.Decimal{"MXN": decimal.NewFromFloat(20.0)}}
	worker := NewWorker(repo, fx, testLogger(), WorkerConfig{
		RetryMaxAttempts: 1,
		PollInterval:     10 * time.Millisecond,
		JobTimeout:       30 * time.Second,
		BatchSize:        10,
		MaxClaimAttempts: 3,
		StaleAfter:       time.Minute,
	})

	id := uuid.New()
	repo.mu.Lock()
	repo.pendingQueue = append(repo.pendingQueue, domain.PendingTask{ID: id, Pair: "EUR/MXN", Attempts: 3})
	repo.mu.Unlock()

	worker.pollOnce(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.calls != 1 {
		t.Fatalf("expected 1 MarkDone/MarkFailed call, got %d", repo.calls)
	}
	if repo.lastStatus != domain.StatusDone {
		t.Fatalf("expected status done (API succeeded), got %s", repo.lastStatus)
	}
	if repo.lastPrice == nil || !repo.lastPrice.Equal(decimal.NewFromFloat(20.0)) {
		t.Fatalf("unexpected price: got %v", repo.lastPrice)
	}
}

// --- collectSymbols tests ---

func TestCollectSymbols(t *testing.T) {
	pairs := map[string]struct{}{
		"EUR/MXN": {},
		"USD/EUR": {},
		"USD/MXN": {},
	}
	got := collectSymbols(pairs)
	if len(got) != 2 {
		t.Fatalf("expected 2 symbols, got %v", got)
	}
	if got[0] != "MXN" || got[1] != "USD" {
		t.Fatalf("expected [MXN USD], got %v", got)
	}
}

func TestCollectSymbols_AllEUR(t *testing.T) {
	pairs := map[string]struct{}{
		"EUR/EUR": {},
	}
	got := collectSymbols(pairs)
	if len(got) != 0 {
		t.Fatalf("expected 0 symbols for EUR/EUR, got %v", got)
	}
}
