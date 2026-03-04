package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"fxquotes/internal/domain"
)

type stubService struct {
	requestUpdateFn func(ctx context.Context, pair string, requestID *string) (*domain.QuoteUpdate, error)
	getUpdateFn     func(ctx context.Context, id uuid.UUID) (*domain.QuoteUpdate, error)
	getLatestFn     func(ctx context.Context, pair string) (*domain.LatestQuote, error)
}

func (s *stubService) RequestUpdate(ctx context.Context, pair string, requestID *string) (*domain.QuoteUpdate, error) {
	return s.requestUpdateFn(ctx, pair, requestID)
}

func (s *stubService) GetUpdate(ctx context.Context, id uuid.UUID) (*domain.QuoteUpdate, error) {
	return s.getUpdateFn(ctx, id)
}

func (s *stubService) GetLatest(ctx context.Context, pair string) (*domain.LatestQuote, error) {
	return s.getLatestFn(ctx, pair)
}

type stubDB struct{}

func (s *stubDB) PingContext(_ context.Context) error { return nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func TestHandleUpdateQuoteSuccess(t *testing.T) {
	calledPair := ""
	svc := &stubService{
		requestUpdateFn: func(_ context.Context, pair string, _ *string) (*domain.QuoteUpdate, error) {
			calledPair = pair
			return &domain.QuoteUpdate{
				ID:        uuid.New(),
				Pair:      "EUR/MXN",
				Status:    domain.StatusPending,
				CreatedAt: time.Now().UTC(),
			}, nil
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	body, _ := json.Marshal(updateQuoteRequest{Pair: "eur/mxn"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/update", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.HandleUpdateQuote(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, resp.StatusCode)
	}
	if calledPair != "eur/mxn" {
		t.Fatalf("expected pair eur/mxn passed to service, got %s", calledPair)
	}
}

func TestHandleUpdateQuoteUnsupportedPair(t *testing.T) {
	svc := &stubService{
		requestUpdateFn: func(_ context.Context, _ string, _ *string) (*domain.QuoteUpdate, error) {
			return nil, domain.ErrUnsupportedPair
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	body, _ := json.Marshal(updateQuoteRequest{Pair: "BTC/USD"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/update", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.HandleUpdateQuote(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Result().StatusCode)
	}
}

func TestHandleGetUpdateSuccess(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC()
	price := decimal.NewFromFloat(1.2345)

	svc := &stubService{
		getUpdateFn: func(_ context.Context, reqID uuid.UUID) (*domain.QuoteUpdate, error) {
			if reqID != id {
				t.Fatalf("expected id %s, got %s", id, reqID)
			}
			return &domain.QuoteUpdate{
				ID:        id,
				Pair:      "EUR/MXN",
				Status:    domain.StatusDone,
				Price:     &price,
				CreatedAt: now.Add(-time.Minute),
				UpdatedAt: &now,
			}, nil
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/update/"+id.String(), nil)
	req.SetPathValue("id", id.String())
	w := httptest.NewRecorder()

	srv.HandleGetUpdate(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var body getUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.ID != id.String() {
		t.Fatalf("expected id %s, got %s", id.String(), body.ID)
	}
	if body.Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", body.Pair)
	}
	if body.Status != string(domain.StatusDone) {
		t.Fatalf("expected status %s, got %s", domain.StatusDone, body.Status)
	}
	if body.Price == nil || !body.Price.Equal(price) {
		t.Fatalf("unexpected price: got %v, expected %s", body.Price, price)
	}
}

func TestHandleGetUpdateNotFound(t *testing.T) {
	svc := &stubService{
		getUpdateFn: func(_ context.Context, _ uuid.UUID) (*domain.QuoteUpdate, error) {
			return nil, domain.ErrNotFound
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/update/"+id.String(), nil)
	req.SetPathValue("id", id.String())
	w := httptest.NewRecorder()

	srv.HandleGetUpdate(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, w.Result().StatusCode)
	}
}

func TestHandleGetLatestSuccess(t *testing.T) {
	now := time.Now().UTC()
	price := decimal.NewFromFloat(25.6789)
	svc := &stubService{
		getLatestFn: func(_ context.Context, _ string) (*domain.LatestQuote, error) {
			return &domain.LatestQuote{
				Pair:      "EUR/MXN",
				Price:     price,
				UpdatedAt: now,
			}, nil
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest?pair=eur/mxn", nil)
	w := httptest.NewRecorder()

	srv.HandleGetLatest(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var body latestQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Pair != "EUR/MXN" {
		t.Fatalf("expected pair EUR/MXN, got %s", body.Pair)
	}
	if !body.Price.Equal(price) {
		t.Fatalf("expected price %s, got %s", price, body.Price)
	}
}

func TestHandleGetLatestNoData(t *testing.T) {
	svc := &stubService{
		getLatestFn: func(_ context.Context, _ string) (*domain.LatestQuote, error) {
			return nil, domain.ErrNotFound
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest?pair=EUR/MXN", nil)
	w := httptest.NewRecorder()

	srv.HandleGetLatest(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, w.Result().StatusCode)
	}
}

func TestHandleGetLatestUnsupportedPair(t *testing.T) {
	svc := &stubService{
		getLatestFn: func(_ context.Context, _ string) (*domain.LatestQuote, error) {
			return nil, domain.ErrUnsupportedPair
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest?pair=ABC/XYZ", nil)
	w := httptest.NewRecorder()

	srv.HandleGetLatest(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d for unsupported pair, got %d", http.StatusBadRequest, w.Result().StatusCode)
	}
}

func TestHandleGetLatestMissingPair(t *testing.T) {
	svc := &stubService{}
	srv := NewServer(svc, &stubDB{}, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest", nil)
	w := httptest.NewRecorder()

	srv.HandleGetLatest(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d for missing pair, got %d", http.StatusBadRequest, w.Result().StatusCode)
	}
}

func TestHandleUpdateQuoteRequestIDPairMismatch(t *testing.T) {
	svc := &stubService{
		requestUpdateFn: func(_ context.Context, _ string, _ *string) (*domain.QuoteUpdate, error) {
			return nil, domain.ErrRequestIDPairMismatch
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	reqID := "same-id"
	body, _ := json.Marshal(updateQuoteRequest{Pair: "USD/EUR", RequestID: &reqID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/update", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.HandleUpdateQuote(w, req)

	if w.Result().StatusCode != http.StatusConflict {
		t.Fatalf("expected status %d, got %d", http.StatusConflict, w.Result().StatusCode)
	}

	var resp errorResponse
	if err := json.NewDecoder(w.Result().Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "request_id already used for a different pair" {
		t.Fatalf("expected error %q, got %q", "request_id already used for a different pair", resp.Error)
	}
}

func TestHandleUpdateQuoteInvalidRequestID(t *testing.T) {
	svc := &stubService{
		requestUpdateFn: func(_ context.Context, _ string, _ *string) (*domain.QuoteUpdate, error) {
			return nil, domain.ErrInvalidRequestID
		},
	}
	srv := NewServer(svc, &stubDB{}, testLogger())

	longID := strings.Repeat("x", 65)
	body, _ := json.Marshal(updateQuoteRequest{Pair: "EUR/MXN", RequestID: &longID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/update", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.HandleUpdateQuote(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Result().StatusCode)
	}

	var resp errorResponse
	if err := json.NewDecoder(w.Result().Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "invalid request_id" {
		t.Fatalf("expected error %q, got %q", "invalid request_id", resp.Error)
	}
}
