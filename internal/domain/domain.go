// Package domain defines shared types and sentinel errors.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Sentinel errors used across layers.
var (
	ErrNotFound               = errors.New("not found")
	ErrUnsupportedPair        = errors.New("unsupported pair")
	ErrInvalidPair            = errors.New("invalid pair format")
	ErrInvalidRequestID       = errors.New("invalid request_id")
	ErrRequestIDPairMismatch  = errors.New("request_id already used for a different pair")
)

// Pair is a normalized currency pair (e.g. "EUR/MXN").
type Pair struct {
	from string
	to   string
}

// ParsePair validates and normalizes a raw pair string.
func ParsePair(raw string) (Pair, error) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return Pair{}, fmt.Errorf("%w: %s", ErrInvalidPair, raw)
	}
	from := strings.TrimSpace(parts[0])
	to := strings.TrimSpace(parts[1])
	if len(from) != 3 || len(to) != 3 {
		return Pair{}, fmt.Errorf("%w: %s", ErrInvalidPair, raw)
	}
	return Pair{from: from, to: to}, nil
}

// From returns the base currency code.
func (p Pair) From() string { return p.from }

// To returns the quote currency code.
func (p Pair) To() string { return p.to }

// String returns the pair in "XXX/YYY" format.
func (p Pair) String() string { return p.from + "/" + p.to }

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
