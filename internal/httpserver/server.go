// Package httpserver provides the HTTP API for the FX quotes service.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"fxquotes/internal/domain"
)

// QuoteService defines the business logic operations used by the HTTP handlers.
type QuoteService interface {
	RequestUpdate(ctx context.Context, pair string, requestID *string) (*domain.QuoteUpdate, error)
	GetUpdate(ctx context.Context, id uuid.UUID) (*domain.QuoteUpdate, error)
	GetLatest(ctx context.Context, pair string) (*domain.LatestQuote, error)
}

// HealthChecker verifies that the database is reachable.
type HealthChecker interface {
	PingContext(ctx context.Context) error
}

// Server handles HTTP requests for the FX quotes API.
type Server struct {
	svc    QuoteService
	db     HealthChecker
	logger *slog.Logger
}

// NewServer creates a Server with the given service, health checker, and logger.
func NewServer(svc QuoteService, db HealthChecker, logger *slog.Logger) *Server {
	return &Server{svc: svc, db: db, logger: logger}
}

type updateQuoteRequest struct {
	Pair      string  `json:"pair"`
	RequestID *string `json:"request_id,omitempty"`
}

type updateQuoteResponse struct {
	UpdateID string `json:"update_id"`
	Status   string `json:"status"`
}

type getUpdateResponse struct {
	ID        string           `json:"id"`
	Pair      string           `json:"pair"`
	Status    string           `json:"status"`
	Price     *decimal.Decimal `json:"price,omitempty"`
	Error     *string          `json:"error,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt *time.Time       `json:"updated_at,omitempty"`
}

type latestQuoteResponse struct {
	Pair      string          `json:"pair"`
	Price     decimal.Decimal `json:"price"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// HandleUpdateQuote creates a new quote update request.
func (s *Server) HandleUpdateQuote(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req updateQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	update, err := s.svc.RequestUpdate(r.Context(), req.Pair, req.RequestID)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.writeJSON(w, http.StatusAccepted, updateQuoteResponse{
		UpdateID: update.ID.String(),
		Status:   string(update.Status),
	})
}

// HandleGetUpdate returns the status and result of a quote update by ID.
func (s *Server) HandleGetUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	u, err := s.svc.GetUpdate(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.writeJSON(w, http.StatusOK, getUpdateResponse{
		ID:        u.ID.String(),
		Pair:      u.Pair,
		Status:    string(u.Status),
		Price:     u.Price,
		Error:     u.Error,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	})
}

// HandleGetLatest returns the most recent completed quote for a currency pair.
func (s *Server) HandleGetLatest(w http.ResponseWriter, r *http.Request) {
	pair := r.URL.Query().Get("pair")
	if strings.TrimSpace(pair) == "" {
		writeError(w, http.StatusBadRequest, "pair is required")
		return
	}

	q, err := s.svc.GetLatest(r.Context(), pair)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}

	s.writeJSON(w, http.StatusOK, latestQuoteResponse{
		Pair:      q.Pair,
		Price:     q.Price,
		UpdatedAt: q.UpdatedAt,
	})
}

// HandleHealth reports the service and database health status.
func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		s.logger.Error("health check: db ping failed", "error", err)
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "db": "down"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "up"})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("failed to encode json response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

func (s *Server) writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrUnsupportedPair):
		writeError(w, http.StatusBadRequest, "unsupported pair")
	case errors.Is(err, domain.ErrInvalidRequestID):
		writeError(w, http.StatusBadRequest, "invalid request_id")
	case errors.Is(err, domain.ErrRequestIDPairMismatch):
		writeError(w, http.StatusConflict, "request_id already used for a different pair")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.logger.Error("internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
