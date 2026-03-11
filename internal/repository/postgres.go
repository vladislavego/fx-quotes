// Package repository provides PostgreSQL-backed storage for quote updates.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"fxquotes/internal/domain"
)

// PostgresQuoteRepository implements QuoteRepository using PostgreSQL.
type PostgresQuoteRepository struct {
	db *sql.DB
}

// NewPostgresQuoteRepository returns a new repository backed by the given database.
func NewPostgresQuoteRepository(db *sql.DB) *PostgresQuoteRepository {
	return &PostgresQuoteRepository{db: db}
}

// FindOrCreatePending inserts a new pending quote or returns an existing one for the given request ID.
func (r *PostgresQuoteRepository) FindOrCreatePending(ctx context.Context, pair string, requestID *string) (*domain.QuoteUpdate, bool, error) {
	now := time.Now().UTC()

	if requestID == nil {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, false, fmt.Errorf("begin tx: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		id := uuid.New()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO quote_updates (id, pair, status, created_at)
			VALUES ($1, $2, $3, $4)
		`, id, pair, domain.StatusPending, now)
		if err != nil {
			return nil, false, fmt.Errorf("insert pending: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO outbox (update_id, pair, created_at)
			VALUES ($1, $2, $3)
		`, id, pair, now)
		if err != nil {
			return nil, false, fmt.Errorf("insert outbox: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit: %w", err)
		}

		return &domain.QuoteUpdate{
			ID:        id,
			Pair:      pair,
			Status:    domain.StatusPending,
			CreatedAt: now,
		}, true, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id := uuid.New()
	var u domain.QuoteUpdate
	err = tx.QueryRowContext(ctx, `
		INSERT INTO quote_updates (id, request_id, pair, status, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (request_id) DO NOTHING
		RETURNING id, request_id, pair, status, price, error, created_at, updated_at
	`, id, *requestID, pair, domain.StatusPending, now).Scan(
		&u.ID, &u.RequestID, &u.Pair, &u.Status, &u.Price, &u.Error, &u.CreatedAt, &u.UpdatedAt,
	)

	switch {
	case err == nil:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO outbox (update_id, pair, created_at)
			VALUES ($1, $2, $3)
		`, u.ID, pair, now)
		if err != nil {
			return nil, false, fmt.Errorf("insert outbox: %w", err)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, false, commitErr
		}
		return &u, true, nil
	case errors.Is(err, sql.ErrNoRows):
		err = tx.QueryRowContext(ctx, `
			SELECT id, request_id, pair, status, price, error, created_at, updated_at
			FROM quote_updates
			WHERE request_id = $1
		`, *requestID).Scan(
			&u.ID, &u.RequestID, &u.Pair, &u.Status, &u.Price, &u.Error, &u.CreatedAt, &u.UpdatedAt,
		)
		if err != nil {
			return nil, false, fmt.Errorf("select by request_id: %w", err)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, false, commitErr
		}
		if u.Pair != pair {
			return nil, false, domain.ErrRequestIDPairMismatch
		}
		return &u, false, nil
	default:
		return nil, false, fmt.Errorf("insert with request_id: %w", err)
	}
}

// GetUpdateByID returns a single quote update by its primary key.
func (r *PostgresQuoteRepository) GetUpdateByID(ctx context.Context, id uuid.UUID) (*domain.QuoteUpdate, error) {
	var u domain.QuoteUpdate
	err := r.db.QueryRowContext(ctx, `
		SELECT id, request_id, pair, status, price, error, created_at, updated_at
		FROM quote_updates
		WHERE id = $1
	`, id).Scan(
		&u.ID, &u.RequestID, &u.Pair, &u.Status, &u.Price, &u.Error, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get update by id: %w", err)
	}
	return &u, nil
}

// GetLatestByPair returns the most recently completed quote for the given pair.
func (r *PostgresQuoteRepository) GetLatestByPair(ctx context.Context, pair string) (*domain.LatestQuote, error) {
	var q domain.LatestQuote
	err := r.db.QueryRowContext(ctx, `
		SELECT pair, price, updated_at
		FROM quote_updates
		WHERE pair = $1 AND status = $2
		ORDER BY updated_at DESC
		LIMIT 1
	`, pair, domain.StatusDone).Scan(&q.Pair, &q.Price, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest by pair: %w", err)
	}
	return &q, nil
}

// MarkDone completes a pending quote with the given price and removes the outbox row.
func (r *PostgresQuoteRepository) MarkDone(ctx context.Context, id uuid.UUID, price decimal.Decimal) error {
	return r.completeTask(ctx, id, domain.StatusDone, &price, nil)
}

// MarkFailed completes a pending quote with the given error and removes the outbox row.
func (r *PostgresQuoteRepository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	return r.completeTask(ctx, id, domain.StatusFailed, nil, &errMsg)
}

func (r *PostgresQuoteRepository) completeTask(ctx context.Context, id uuid.UUID, status domain.QuoteStatus, price *decimal.Decimal, errMsg *string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `
		UPDATE quote_updates
		SET status = $2,
		    price = $3,
		    error = $4,
		    updated_at = $5
		WHERE id = $1 AND status = $6
	`, id, status, price, errMsg, now, domain.StatusPending)
	if err != nil {
		return fmt.Errorf("update quote status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM outbox WHERE update_id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete outbox row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ClaimBatch claims up to limit unclaimed or stale outbox tasks.
func (r *PostgresQuoteRepository) ClaimBatch(ctx context.Context, limit int, staleAfter time.Duration) ([]domain.PendingTask, error) {
	staleBefore := time.Now().UTC().Add(-staleAfter)

	rows, err := r.db.QueryContext(ctx, `
		WITH claimable AS (
			SELECT update_id FROM outbox
			WHERE (claimed_at IS NULL OR claimed_at < $2)
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		),
		claimed AS (
			UPDATE outbox SET attempts = attempts + 1, claimed_at = NOW()
			FROM claimable WHERE outbox.update_id = claimable.update_id
			RETURNING outbox.update_id, outbox.pair, outbox.attempts
		)
		SELECT update_id, pair, attempts FROM claimed
	`, limit, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("claim batch: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []domain.PendingTask
	for rows.Next() {
		var t domain.PendingTask
		if err := rows.Scan(&t.ID, &t.Pair, &t.Attempts); err != nil {
			return nil, fmt.Errorf("scan claimed task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed tasks: %w", err)
	}
	return tasks, nil
}
