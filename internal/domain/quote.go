package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// QuoteStatus represents the lifecycle state of a quote update.
type QuoteStatus string

// Quote update lifecycle states.
const (
	StatusPending QuoteStatus = "pending"
	StatusDone    QuoteStatus = "done"
	StatusFailed  QuoteStatus = "failed"
)

// PendingTask is a claimed outbox task ready for processing.
type PendingTask struct {
	ID       uuid.UUID
	Pair     string
	Attempts int
}

// QuoteUpdate represents a quote update request and its result.
type QuoteUpdate struct {
	ID        uuid.UUID
	RequestID *string
	Pair      string
	Status    QuoteStatus
	Price     *decimal.Decimal
	Error     *string
	CreatedAt time.Time
	UpdatedAt *time.Time
}

// LatestQuote is a completed quote with guaranteed non-nil price and timestamp.
type LatestQuote struct {
	Pair      string
	Price     decimal.Decimal
	UpdatedAt time.Time
}
