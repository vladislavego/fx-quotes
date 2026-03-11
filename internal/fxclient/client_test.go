package fxclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetRates_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("access_key"); got != "test-key" {
			t.Fatalf("expected access_key=test-key, got %s", got)
		}
		if got := r.URL.Query().Get("symbols"); got != "MXN,USD" {
			t.Fatalf("expected symbols=MXN,USD, got %s", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse{
			Success: true,
			Base:    "EUR",
			Rates: map[string]json.Number{
				"USD": json.Number("1.1"),
				"MXN": json.Number("20.345678"),
			},
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "test-key", 5*time.Second)
	rates, err := client.GetRates(context.Background(), []string{"MXN", "USD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rates) != 2 {
		t.Fatalf("expected 2 rates, got %d", len(rates))
	}
	if rates["USD"].String() != "1.1" {
		t.Fatalf("expected USD=1.1, got %s", rates["USD"])
	}
	if rates["MXN"].String() != "20.345678" {
		t.Fatalf("expected MXN=20.345678, got %s", rates["MXN"])
	}
}

func TestGetRates_HTTP4xx_Permanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "test-key", 5*time.Second)
	_, err := client.GetRates(context.Background(), []string{"USD"})
	if err == nil {
		t.Fatal("expected error for 403")
	}

	var pe *PermanentError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PermanentError, got %T: %v", err, err)
	}
}

func TestGetRates_HTTP429_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "test-key", 5*time.Second)
	_, err := client.GetRates(context.Background(), []string{"USD"})
	if err == nil {
		t.Fatal("expected error for 429")
	}

	var pe *PermanentError
	if errors.As(err, &pe) {
		t.Fatal("429 should NOT be permanent")
	}
}

func TestGetRates_HTTP5xx_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "test-key", 5*time.Second)
	_, err := client.GetRates(context.Background(), []string{"USD"})
	if err == nil {
		t.Fatal("expected error for 500")
	}

	var pe *PermanentError
	if errors.As(err, &pe) {
		t.Fatal("500 should NOT be permanent")
	}
}

func TestGetRates_SuccessFalse_Permanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse{
			Success: false,
			Error: &apiError{
				Code: 101,
				Type: "invalid_access_key",
				Info: "You have not supplied a valid API Access Key.",
			},
		})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "test-key", 5*time.Second)
	_, err := client.GetRates(context.Background(), []string{"USD"})
	if err == nil {
		t.Fatal("expected error for success=false")
	}

	var pe *PermanentError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PermanentError for success=false, got %T: %v", err, err)
	}
}

func TestGetRates_EmptyAPIKey(t *testing.T) {
	client := NewHTTPClient("http://example.com", "", 5*time.Second)
	_, err := client.GetRates(context.Background(), []string{"USD"})
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}
