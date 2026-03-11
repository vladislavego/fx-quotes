// Package fxclient provides an HTTP client for the exchangeratesapi.io API.
package fxclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// HTTPClient fetches EUR-based exchange rates from an external API.
type HTTPClient struct {
	baseURL string
	client  *http.Client
	apiKey  string
}

// NewHTTPClient creates a client for the given base URL, API key, and request timeout.
func NewHTTPClient(baseURL, apiKey string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: timeout,
		},
		apiKey: apiKey,
	}
}

type apiResponse struct {
	Success   bool                   `json:"success"`
	Timestamp int64                  `json:"timestamp"`
	Base      string                 `json:"base"`
	Date      string                 `json:"date"`
	Rates     map[string]json.Number `json:"rates"`
	Error     *apiError              `json:"error,omitempty"`
}

type apiError struct {
	Code int    `json:"code"`
	Type string `json:"type"`
	Info string `json:"info"`
}

// PermanentError represents an error that should not be retried.
type PermanentError struct {
	Err error
}

// Error implements the error interface.
func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent signals that the error is not transient.
func (e *PermanentError) Permanent() bool { return true }

// GetRates fetches EUR-based rates for the given currency symbols in a single API call.
func (c *HTTPClient) GetRates(ctx context.Context, symbols []string) (map[string]decimal.Decimal, error) {
	if c.apiKey == "" {
		return nil, &PermanentError{Err: errors.New("fx api key is not set")}
	}

	u, err := url.Parse(c.baseURL + "/latest")
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("access_key", c.apiKey)
	if len(symbols) > 0 {
		q.Set("symbols", strings.Join(symbols, ","))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req) //nolint:gosec // URL is built from configured base URL, not user input
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("fx api returned status %d", resp.StatusCode)
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, &PermanentError{Err: err}
		}
		return nil, err
	}

	const maxResponseSize = 1 << 20 // 1 MB
	var body apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&body); err != nil {
		return nil, err
	}

	if !body.Success {
		var err error
		if body.Error != nil {
			err = fmt.Errorf("fx api error: code=%d type=%s info=%s", body.Error.Code, body.Error.Type, body.Error.Info)
		} else {
			err = errors.New("fx api returned success=false")
		}
		return nil, &PermanentError{Err: err}
	}

	rates := make(map[string]decimal.Decimal, len(body.Rates))
	for k, v := range body.Rates {
		d, err := decimal.NewFromString(v.String())
		if err != nil {
			return nil, fmt.Errorf("invalid rate for %s: %w", k, err)
		}
		rates[k] = d
	}

	return rates, nil
}
