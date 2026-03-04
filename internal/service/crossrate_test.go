package service

import (
	"testing"

	"github.com/shopspring/decimal"
)

func mustDec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("bad test decimal %q: %v", s, err)
	}
	return d
}

func TestCrossRate(t *testing.T) {
	rates := map[string]decimal.Decimal{
		"USD": mustDec(t, "1.1"),
		"MXN": mustDec(t, "20.0"),
		"GBP": mustDec(t, "0.85"),
	}

	tests := []struct {
		name    string
		from    string
		to      string
		rates   map[string]decimal.Decimal
		want    decimal.Decimal
		wantErr bool
	}{
		{
			name:  "same currency",
			from:  "EUR",
			to:    "EUR",
			rates: rates,
			want:  decimal.NewFromInt(1),
		},
		{
			name:  "EUR to other",
			from:  "EUR",
			to:    "USD",
			rates: rates,
			want:  mustDec(t, "1.1"),
		},
		{
			name:  "other to EUR",
			from:  "USD",
			to:    "EUR",
			rates: rates,
			want:  decimal.NewFromInt(1).Div(mustDec(t, "1.1")),
		},
		{
			name:  "cross rate USD to MXN",
			from:  "USD",
			to:    "MXN",
			rates: rates,
			want:  mustDec(t, "20.0").Div(mustDec(t, "1.1")),
		},
		{
			name:  "cross rate MXN to GBP",
			from:  "MXN",
			to:    "GBP",
			rates: rates,
			want:  mustDec(t, "0.85").Div(mustDec(t, "20.0")),
		},
		{
			name:    "missing from currency",
			from:    "JPY",
			to:      "USD",
			rates:   rates,
			wantErr: true,
		},
		{
			name:    "missing to currency",
			from:    "USD",
			to:      "JPY",
			rates:   rates,
			wantErr: true,
		},
		{
			name:    "zero base rate",
			from:    "USD",
			to:      "MXN",
			rates:   map[string]decimal.Decimal{"USD": decimal.NewFromInt(0), "MXN": mustDec(t, "20.0")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := crossRate(tt.from, tt.to, tt.rates)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got rate %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("expected rate %s, got %s", tt.want, got)
			}
		})
	}
}

func TestCollectSymbols(t *testing.T) {
	pairs := map[string]struct{}{
		"EUR/MXN": {},
		"USD/EUR": {},
		"USD/MXN": {},
	}
	got := collectSymbols(pairs)
	if len(got) != 2 {
		t.Fatalf("expected 2 symbols, got %v", got)
	}
	if got[0] != "MXN" || got[1] != "USD" {
		t.Fatalf("expected [MXN USD], got %v", got)
	}
}

func TestCollectSymbols_AllEUR(t *testing.T) {
	pairs := map[string]struct{}{
		"EUR/EUR": {},
	}
	got := collectSymbols(pairs)
	if len(got) != 0 {
		t.Fatalf("expected 0 symbols for EUR/EUR, got %v", got)
	}
}
