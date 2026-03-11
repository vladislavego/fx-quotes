package domain

import (
	"fmt"
	"strings"
)

// Pair is a normalized currency pair (e.g. "EUR/MXN").
type Pair struct {
	from string
	to   string
}

// ParsePair validates and normalizes a raw pair string ("FROM/TO").
// Actual currency validation is done by the allowedPairs whitelist in service.
// For full ISO 4217 validation consider bojanz/currency or go-playground/validator.
func ParsePair(raw string) (Pair, error) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return Pair{}, fmt.Errorf("%w: %s", ErrInvalidPair, raw)
	}
	from := strings.TrimSpace(parts[0])
	to := strings.TrimSpace(parts[1])
	if from == "" || to == "" {
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
