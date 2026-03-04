package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"fxquotes/internal/domain"
)

// WorkerRepository defines persistence operations for background processing.
// ClaimBatch claims unclaimed/stale outbox tasks (outbox-only, domain table untouched).
// MarkDone/MarkFailed transition pending→done/failed and delete the outbox row (idempotent).
type WorkerRepository interface {
	ClaimBatch(ctx context.Context, limit int, staleAfter time.Duration) ([]domain.PendingTask, error)
	MarkDone(ctx context.Context, id uuid.UUID, price decimal.Decimal) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
}

// RateProvider fetches EUR-based exchange rates from an external source.
type RateProvider interface {
	GetRates(ctx context.Context, symbols []string) (map[string]decimal.Decimal, error)
}

const dbOpTimeout = 5 * time.Second

func isPermanent(err error) bool {
	var pe interface{ Permanent() bool }
	return errors.As(err, &pe) && pe.Permanent()
}

// WorkerConfig holds tuning parameters for background processing.
type WorkerConfig struct {
	RetryMaxAttempts int
	RetryBaseDelay   time.Duration
	PollInterval     time.Duration
	JobTimeout       time.Duration
	BatchSize        int
	MaxClaimAttempts int
	StaleAfter       time.Duration
}

// Worker polls the outbox and processes pending quote updates in batches.
type Worker struct {
	repo   WorkerRepository
	fx     RateProvider
	logger *slog.Logger
	cfg    WorkerConfig
}

// NewWorker creates a Worker with the given dependencies and settings.
func NewWorker(repo WorkerRepository, fx RateProvider, logger *slog.Logger, cfg WorkerConfig) *Worker {
	return &Worker{repo: repo, fx: fx, logger: logger, cfg: cfg}
}

// Run polls for pending tasks and processes them in batches until ctx is cancelled.
// Panics are recovered and the worker restarts automatically.
func (w *Worker) Run(ctx context.Context) {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					w.logger.Error("worker: recovered from panic",
						"panic", r, "stack", string(debug.Stack()))
				}
			}()
			w.runLoop(ctx)
		}()

		if ctx.Err() != nil {
			return
		}
		w.logger.Warn("worker: restarting after panic")
		time.Sleep(time.Second)
	}
}

func (w *Worker) runLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		w.pollOnce(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) pollOnce(ctx context.Context) {
	tasks, err := w.repo.ClaimBatch(ctx, w.cfg.BatchSize, w.cfg.StaleAfter)
	if err != nil {
		w.logger.Error("worker: failed to claim batch", "error", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	w.processBatch(ctx, tasks)
}

func (w *Worker) processBatch(ctx context.Context, tasks []domain.PendingTask) {
	w.logger.Info("worker: processing batch", "count", len(tasks))

	pairSet := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		pairSet[t.Pair] = struct{}{}
	}

	symbols := collectSymbols(pairSet)

	jobCtx, cancel := context.WithTimeout(ctx, w.cfg.JobTimeout)
	defer cancel()

	var rates map[string]decimal.Decimal
	var err error

	for attempt := 0; attempt < w.cfg.RetryMaxAttempts; attempt++ {
		if attempt > 0 {
			delay := w.cfg.RetryBaseDelay * (1 << (attempt - 1))
			w.logger.Warn("worker: retrying GetRates", "attempt", attempt+1, "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-jobCtx.Done():
				timer.Stop()
				w.logger.Warn("worker: job timed out, leaving tasks for re-claim")
				return
			}
		}

		rates, err = w.fx.GetRates(jobCtx, symbols)
		if err == nil {
			break
		}
		w.logger.Warn("worker: GetRates failed", "attempt", attempt+1, "error", err)
		if isPermanent(err) {
			w.logger.Error("worker: permanent error, skipping retries", "error", err)
			break
		}
	}

	// Use WithoutCancel so DB writes survive graceful shutdown (SIGTERM).
	// Each task gets its own 5 s timeout so a slow write doesn't starve the rest.
	dbParent := context.WithoutCancel(ctx)

	if err != nil {
		if isPermanent(err) {
			w.logger.Error("worker: permanent error, failing batch", "error", err)
			w.failBatch(dbParent, tasks, err.Error())
		} else {
			var exhausted []domain.PendingTask
			for _, t := range tasks {
				if t.Attempts >= w.cfg.MaxClaimAttempts {
					exhausted = append(exhausted, t)
				}
			}
			if len(exhausted) > 0 {
				w.failBatch(dbParent, exhausted, fmt.Sprintf("max attempts exceeded: %v", err))
			}
			w.logger.Warn("worker: transient error, leaving tasks for re-claim", "error", err)
		}
		return
	}

	for _, t := range tasks {
		p, err := domain.ParsePair(t.Pair)
		if err != nil {
			msg := fmt.Sprintf("invalid pair in db: %s (%v)", t.Pair, err)
			w.logger.Error("worker: invalid pair in task", "update_id", t.ID, "pair", t.Pair, "error", err)
			w.markFailed(dbParent, t.ID, msg)
			continue
		}
		rate, err := crossRate(p.From(), p.To(), rates)
		if err != nil {
			msg := fmt.Sprintf("cross rate error: %v", err)
			w.logger.Error("worker: cross rate failed", "update_id", t.ID, "pair", t.Pair, "error", err)
			w.markFailed(dbParent, t.ID, msg)
			continue
		}

		if err := w.markDone(dbParent, t.ID, rate); err != nil {
			continue
		}
		w.logger.Info("worker: successfully updated", "update_id", t.ID, "pair", t.Pair, "rate", rate)
	}
}

func (w *Worker) failBatch(ctx context.Context, tasks []domain.PendingTask, errMsg string) {
	for _, t := range tasks {
		w.markFailed(ctx, t.ID, errMsg)
	}
}

func (w *Worker) markDone(parent context.Context, id uuid.UUID, rate decimal.Decimal) error {
	ctx, cancel := context.WithTimeout(parent, dbOpTimeout)
	defer cancel()

	if err := w.repo.MarkDone(ctx, id, rate); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			w.logger.Warn("worker: task already completed", "update_id", id)
		case errors.Is(err, context.DeadlineExceeded):
			w.logger.Error("worker: MarkDone timed out", "update_id", id, "error", err)
		case errors.Is(err, context.Canceled):
			w.logger.Warn("worker: MarkDone canceled", "update_id", id, "error", err)
		default:
			w.logger.Error("worker: failed to mark task done", "update_id", id, "error", err)
		}
		return err
	}
	return nil
}

func (w *Worker) markFailed(parent context.Context, id uuid.UUID, errMsg string) {
	ctx, cancel := context.WithTimeout(parent, dbOpTimeout)
	defer cancel()

	if err := w.repo.MarkFailed(ctx, id, errMsg); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			w.logger.Warn("worker: task already completed", "update_id", id)
		case errors.Is(err, context.DeadlineExceeded):
			w.logger.Error("worker: MarkFailed timed out", "update_id", id, "error", err)
		case errors.Is(err, context.Canceled):
			w.logger.Warn("worker: MarkFailed canceled", "update_id", id, "error", err)
		default:
			w.logger.Error("worker: failed to mark task failed", "update_id", id, "error", err)
		}
	}
}
