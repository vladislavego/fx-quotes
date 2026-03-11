package domain

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

func mustPair(t *testing.T, from, to string) Pair {
	t.Helper()
	return Pair{from: from, to: to}
}

func TestCrossRate(t *testing.T) {
	rates := map[string]decimal.Decimal{
		"USD": mustDec(t, "1.1"),
		"MXN": mustDec(t, "20.0"),
		"GBP": mustDec(t, "0.85"),
	}

	tests := []struct {
		name    string
		pair    Pair
		rates   map[string]decimal.Decimal
		want    decimal.Decimal
		wantErr bool
	}{
		{
			name:  "same currency",
			pair:  mustPair(t, "EUR", "EUR"),
			rates: rates,
			want:  decimal.NewFromInt(1),
		},
		{
			name:  "EUR to other",
			pair:  mustPair(t, "EUR", "USD"),
			rates: rates,
			want:  mustDec(t, "1.1"),
		},
		{
			name:  "other to EUR",
			pair:  mustPair(t, "USD", "EUR"),
			rates: rates,
			want:  decimal.NewFromInt(1).Div(mustDec(t, "1.1")),
		},
		{
			name:  "cross rate USD to MXN",
			pair:  mustPair(t, "USD", "MXN"),
			rates: rates,
			want:  mustDec(t, "20.0").Div(mustDec(t, "1.1")),
		},
		{
			name:  "cross rate MXN to GBP",
			pair:  mustPair(t, "MXN", "GBP"),
			rates: rates,
			want:  mustDec(t, "0.85").Div(mustDec(t, "20.0")),
		},
		{
			name:    "missing from currency",
			pair:    mustPair(t, "JPY", "USD"),
			rates:   rates,
			wantErr: true,
		},
		{
			name:    "missing to currency",
			pair:    mustPair(t, "USD", "JPY"),
			rates:   rates,
			wantErr: true,
		},
		{
			name:    "zero base rate",
			pair:    mustPair(t, "USD", "MXN"),
			rates:   map[string]decimal.Decimal{"USD": decimal.NewFromInt(0), "MXN": mustDec(t, "20.0")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CrossRate(tt.pair, tt.rates)
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
