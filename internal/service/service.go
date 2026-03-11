// Package service implements the business logic for FX quote updates.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"fxquotes/internal/domain"
)

// QuoteRepository defines the persistence operations used by QuoteService.
// FindOrCreatePending returns (update, created=true) for new records
// and (existing, created=false) when requestID matches the same pair.
// It returns domain.ErrRequestIDPairMismatch when requestID was used for a different pair.
type QuoteRepository interface {
	FindOrCreatePending(ctx context.Context, pair string, requestID *string) (*domain.QuoteUpdate, bool, error)
	GetUpdateByID(ctx context.Context, id uuid.UUID) (*domain.QuoteUpdate, error)
	GetLatestByPair(ctx context.Context, pair string) (*domain.LatestQuote, error)
}

const maxRequestIDLen = 64

func normalizeRequestID(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" || len(trimmed) > maxRequestIDLen {
		return nil, domain.ErrInvalidRequestID
	}
	return &trimmed, nil
}

var allowedPairs = map[string]struct{}{
	"USD/EUR": {},
	"EUR/USD": {},
	"EUR/MXN": {},
	"USD/MXN": {},
	"MXN/EUR": {},
	"MXN/USD": {},
}

func validatePair(raw string) (domain.Pair, error) {
	p, err := domain.ParsePair(raw)
	if err != nil {
		return domain.Pair{}, domain.ErrUnsupportedPair
	}
	if _, ok := allowedPairs[p.String()]; !ok {
		return domain.Pair{}, domain.ErrUnsupportedPair
	}
	return p, nil
}

// QuoteService handles HTTP-facing business logic for quote update requests.
type QuoteService struct {
	repo QuoteRepository
}

// NewQuoteService creates a QuoteService with the given repository.
func NewQuoteService(repo QuoteRepository) *QuoteService {
	return &QuoteService{repo: repo}
}

// RequestUpdate validates the pair and request ID, then creates a pending quote update.
// When a request_id is reused with a different pair, domain.ErrRequestIDPairMismatch is returned.
func (s *QuoteService) RequestUpdate(ctx context.Context, rawPair string, requestID *string) (*domain.QuoteUpdate, error) {
	reqID, err := normalizeRequestID(requestID)
	if err != nil {
		return nil, err
	}
	p, err := validatePair(rawPair)
	if err != nil {
		return nil, err
	}

	update, _, err := s.repo.FindOrCreatePending(ctx, p.String(), reqID)
	if err != nil {
		return nil, fmt.Errorf("create update: %w", err)
	}

	return update, nil
}

// GetUpdate returns a quote update by its ID.
func (s *QuoteService) GetUpdate(ctx context.Context, id uuid.UUID) (*domain.QuoteUpdate, error) {
	return s.repo.GetUpdateByID(ctx, id)
}

// GetLatest returns the most recent completed quote for the given pair.
func (s *QuoteService) GetLatest(ctx context.Context, rawPair string) (*domain.LatestQuote, error) {
	p, err := validatePair(rawPair)
	if err != nil {
		return nil, err
	}
	return s.repo.GetLatestByPair(ctx, p.String())
}
