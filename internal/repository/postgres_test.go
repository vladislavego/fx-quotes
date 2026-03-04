//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/shopspring/decimal"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"fxquotes/internal/domain"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	initScript, err := filepath.Abs("../../db/init/001_init.sql")
	if err != nil {
		panic("resolve init script path: " + err.Error())
	}

	pg, err := postgres.Run(ctx,
		"postgres:16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.WithInitScripts(initScript),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}

	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("get connection string: " + err.Error())
	}

	testDB, err = sql.Open("pgx", connStr)
	if err != nil {
		panic("open db: " + err.Error())
	}

	for i := range 10 {
		if err := testDB.PingContext(ctx); err == nil {
			break
		} else if i == 9 {
			panic("ping db after 10 attempts: " + err.Error())
		}
		time.Sleep(500 * time.Millisecond)
	}

	code := m.Run()

	testDB.Close()
	_ = pg.Terminate(ctx)
	os.Exit(code)
}

func truncate(t *testing.T) {
	t.Helper()
	_, err := testDB.ExecContext(context.Background(), "TRUNCATE quote_updates CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

const defaultStaleAfter = time.Minute

func mustClaimBatch(t *testing.T, repo *PostgresQuoteRepository, limit int) []domain.PendingTask {
	t.Helper()
	tasks, err := repo.ClaimBatch(context.Background(), limit, defaultStaleAfter)
	if err != nil {
		t.Fatalf("claim batch: %v", err)
	}
	return tasks
}

func TestFindOrCreatePending_CreateNew(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, created, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for new record")
	}
	if u.Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", u.Pair)
	}
	if u.Status != domain.StatusPending {
		t.Fatalf("expected status pending, got %s", u.Status)
	}
}

func TestFindOrCreatePending_NoDedupByPair(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	first, created1, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !created1 {
		t.Fatal("expected first call to create")
	}

	second, created2, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !created2 {
		t.Fatal("expected second call to create new record")
	}
	if second.ID == first.ID {
		t.Fatalf("expected different IDs, both got %s", first.ID)
	}
}

func TestFindOrCreatePending_DifferentPairsNotDeduped(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u1, created1, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !created1 {
		t.Fatal("expected created for EUR/MXN")
	}

	u2, created2, err := repo.FindOrCreatePending(ctx, "USD/EUR", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !created2 {
		t.Fatal("expected created for USD/EUR")
	}
	if u1.ID == u2.ID {
		t.Fatal("expected different IDs for different pairs")
	}
}

func TestFindOrCreatePending_IdempotentWithRequestID(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	reqID := "idempotent-123"
	first, created1, err := repo.FindOrCreatePending(ctx, "EUR/MXN", &reqID)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !created1 {
		t.Fatal("expected first call to create")
	}

	second, created2, err := repo.FindOrCreatePending(ctx, "EUR/MXN", &reqID)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created2 {
		t.Fatal("expected second call to return existing")
	}
	if second.ID != first.ID {
		t.Fatalf("expected same ID %s, got %s", first.ID, second.ID)
	}
}

func TestFindOrCreatePending_RequestIDPairMismatch(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	reqID := "mismatch-test"
	_, created, err := repo.FindOrCreatePending(ctx, "EUR/MXN", &reqID)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !created {
		t.Fatal("expected first call to create")
	}

	_, _, err = repo.FindOrCreatePending(ctx, "USD/EUR", &reqID)
	if !errors.Is(err, domain.ErrRequestIDPairMismatch) {
		t.Fatalf("expected ErrRequestIDPairMismatch, got %v", err)
	}
}

func TestFindOrCreatePending_CreatesOutboxRow(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var count int
	err = testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE update_id = $1", u.ID).Scan(&count)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 outbox row, got %d", count)
	}
}

func TestClaimBatch_ReturnsPending(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	created, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	tasks := mustClaimBatch(t, repo, 10)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != created.ID {
		t.Fatalf("expected ID %s, got %s", created.ID, tasks[0].ID)
	}
	if tasks[0].Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", tasks[0].Pair)
	}
}

func TestClaimBatch_StatusStaysPending(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mustClaimBatch(t, repo, 10)

	// Domain status must remain 'pending' — ClaimBatch is outbox-only.
	got, err := repo.GetUpdateByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.StatusPending {
		t.Fatalf("expected status pending after claim, got %s", got.Status)
	}
}

func TestClaimBatch_ReturnsEmptyWhenNoPending(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)

	tasks := mustClaimBatch(t, repo, 10)
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestClaimBatch_SkipsDoneRecords(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	price := decimal.NewFromFloat(20.5)
	if err := repo.MarkDone(ctx, u.ID, price); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Outbox row deleted by MarkDone — nothing to claim.
	tasks := mustClaimBatch(t, repo, 10)
	if len(tasks) != 0 {
		t.Fatal("expected 0 tasks — done records should not be claimed")
	}
}

func TestClaimBatch_RespectsLimit(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	for range 5 {
		_, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	tasks := mustClaimBatch(t, repo, 3)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks with limit=3, got %d", len(tasks))
	}

	// Remaining 2 are still pending (not yet claimed).
	tasks = mustClaimBatch(t, repo, 10)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 remaining tasks, got %d", len(tasks))
	}
}

func TestClaimBatch_KeepsOutboxRows(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	_, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mustClaimBatch(t, repo, 10)

	var count int
	err = testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox").Scan(&count)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 outbox row after claim (not deleted), got %d", count)
	}
}

func TestClaimBatch_ReclaimsStale(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// First claim — sets claimed_at.
	tasks := mustClaimBatch(t, repo, 10)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task on first claim, got %d", len(tasks))
	}

	// Verify task is now claimed and not re-claimable with long staleAfter.
	tasks, err = repo.ClaimBatch(ctx, 10, time.Hour)
	if err != nil {
		t.Fatalf("claim batch: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks with long staleAfter, got %d", len(tasks))
	}

	// Backdate claimed_at to make the task appear stale.
	_, err = testDB.ExecContext(ctx,
		"UPDATE outbox SET claimed_at = NOW() - INTERVAL '10 minutes' WHERE update_id = $1", u.ID)
	if err != nil {
		t.Fatalf("backdate claimed_at: %v", err)
	}

	// Re-claim with short staleAfter — should pick up the stale task.
	tasks, err = repo.ClaimBatch(ctx, 10, 5*time.Second)
	if err != nil {
		t.Fatalf("claim batch: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 re-claimed stale task, got %d", len(tasks))
	}
	if tasks[0].ID != u.ID {
		t.Fatalf("expected ID %s, got %s", u.ID, tasks[0].ID)
	}
}

func TestClaimBatch_ReturnsAttempts(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// First claim — attempts should be 1.
	tasks := mustClaimBatch(t, repo, 10)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Attempts != 1 {
		t.Fatalf("expected attempts=1 on first claim, got %d", tasks[0].Attempts)
	}

	// Backdate claimed_at to allow re-claim.
	_, err = testDB.ExecContext(ctx,
		"UPDATE outbox SET claimed_at = NOW() - INTERVAL '10 minutes' WHERE update_id = $1", u.ID)
	if err != nil {
		t.Fatalf("backdate claimed_at: %v", err)
	}

	// Second claim — attempts should be 2.
	tasks, err = repo.ClaimBatch(ctx, 10, 5*time.Second)
	if err != nil {
		t.Fatalf("claim batch: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Attempts != 2 {
		t.Fatalf("expected attempts=2 on second claim, got %d", tasks[0].Attempts)
	}
}

func TestClaimBatch_SkipsDoneAndFailed(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	// Create and complete a task as done.
	uDone, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	price := decimal.NewFromFloat(20.5)
	if err := repo.MarkDone(ctx, uDone.ID, price); err != nil {
		t.Fatalf("complete done: %v", err)
	}

	// Create and complete a task as failed.
	uFailed, _, err := repo.FindOrCreatePending(ctx, "USD/EUR", nil)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := repo.MarkFailed(ctx, uFailed.ID, "some error"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	// Outbox rows deleted by MarkDone/MarkFailed — nothing to claim.
	tasks, err := repo.ClaimBatch(ctx, 10, time.Nanosecond)
	if err != nil {
		t.Fatalf("claim batch: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks (done/failed not reclaimable), got %d", len(tasks))
	}
}

func TestMarkDone_Success(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	price := decimal.NewFromFloat(20.531822)
	if err := repo.MarkDone(ctx, u.ID, price); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	got, err := repo.GetUpdateByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.StatusDone {
		t.Fatalf("expected status done, got %s", got.Status)
	}
	if got.Price == nil || !got.Price.Equal(price) {
		t.Fatalf("expected price %s, got %v", price, got.Price)
	}
	if got.UpdatedAt == nil {
		t.Fatal("expected updated_at to be set")
	}

	var count int
	err = testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE update_id = $1", u.ID).Scan(&count)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 outbox rows after MarkDone, got %d", count)
	}
}

func TestMarkFailed_Success(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	errMsg := "api timeout"
	if err := repo.MarkFailed(ctx, u.ID, errMsg); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	got, err := repo.GetUpdateByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.StatusFailed {
		t.Fatalf("expected status failed, got %s", got.Status)
	}
	if got.Error == nil || *got.Error != errMsg {
		t.Fatalf("expected error %q, got %v", errMsg, got.Error)
	}

	var count int
	err = testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE update_id = $1", u.ID).Scan(&count)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 outbox rows after MarkFailed, got %d", count)
	}
}

func TestMarkFailed_NotFound(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)

	err := repo.MarkFailed(context.Background(), uuid.New(), "some error")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent ID, got %v", err)
	}
}

func TestGetUpdateByID_NotFound(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)

	_, err := repo.GetUpdateByID(context.Background(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetLatestByPair_ReturnsDone(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	price := decimal.NewFromFloat(20.5)
	if err := repo.MarkDone(ctx, u.ID, price); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	got, err := repo.GetLatestByPair(ctx, "EUR/MXN")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got.Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", got.Pair)
	}
	if !got.Price.Equal(price) {
		t.Fatalf("expected price %s, got %s", price, got.Price)
	}
}

func TestGetLatestByPair_IgnoresPending(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	if _, _, err := repo.FindOrCreatePending(ctx, "EUR/MXN", nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := repo.GetLatestByPair(ctx, "EUR/MXN")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for pending-only pair, got %v", err)
	}
}

func TestGetLatestByPair_NotFound(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)

	_, err := repo.GetLatestByPair(context.Background(), "USD/EUR")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetLatestByPair_ReturnsNewest(t *testing.T) {
	truncate(t)
	repo := NewPostgresQuoteRepository(testDB)
	ctx := context.Background()

	u1, _, _ := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	price1 := decimal.NewFromFloat(20.0)
	_ = repo.MarkDone(ctx, u1.ID, price1)

	time.Sleep(10 * time.Millisecond)

	u2, _, _ := repo.FindOrCreatePending(ctx, "EUR/MXN", nil)
	price2 := decimal.NewFromFloat(21.0)
	_ = repo.MarkDone(ctx, u2.ID, price2)

	got, err := repo.GetLatestByPair(ctx, "EUR/MXN")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if !got.Price.Equal(price2) {
		t.Fatalf("expected latest price %s, got %s", price2, got.Price)
	}
}

func TestADRCheck_DoneRequiresPrice(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	id := uuid.New()
	// status=done with price=NULL must be rejected by the CHECK constraint.
	_, err := testDB.ExecContext(ctx, `
		INSERT INTO quote_updates (id, pair, status, created_at)
		VALUES ($1, 'EUR/MXN', 'done', NOW())
	`, id)
	if err == nil {
		t.Fatal("expected CHECK violation for done with NULL price")
	}
}

func TestADRCheck_FailedRequiresError(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	id := uuid.New()
	// status=failed with error=NULL must be rejected.
	_, err := testDB.ExecContext(ctx, `
		INSERT INTO quote_updates (id, pair, status, created_at)
		VALUES ($1, 'EUR/MXN', 'failed', NOW())
	`, id)
	if err == nil {
		t.Fatal("expected CHECK violation for failed with NULL error")
	}
}

func TestADRCheck_PendingRejectsPrice(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	id := uuid.New()
	// status=pending with price set must be rejected.
	_, err := testDB.ExecContext(ctx, `
		INSERT INTO quote_updates (id, pair, status, price, created_at)
		VALUES ($1, 'EUR/MXN', 'pending', 42.0, NOW())
	`, id)
	if err == nil {
		t.Fatal("expected CHECK violation for pending with price set")
	}
}

func TestADRCheck_RejectsProcessingStatus(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	id := uuid.New()
	// 'processing' is no longer a valid status.
	_, err := testDB.ExecContext(ctx, `
		INSERT INTO quote_updates (id, pair, status, created_at)
		VALUES ($1, 'EUR/MXN', 'processing', NOW())
	`, id)
	if err == nil {
		t.Fatal("expected CHECK violation for 'processing' status")
	}
}
