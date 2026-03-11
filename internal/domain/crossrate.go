package domain

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// CrossRate computes the exchange rate between two currencies
// using EUR-based rates from the external API.
// Formula: rate(FROM/TO) = eurToTO / eurToFROM.
func CrossRate(pair Pair, rates map[string]decimal.Decimal) (decimal.Decimal, error) {
	from, to := pair.From(), pair.To()
	zero := decimal.Decimal{}
	if from == to {
		return decimal.NewFromInt(1), nil
	}

	var eurToFrom decimal.Decimal
	if from == "EUR" {
		eurToFrom = decimal.NewFromInt(1)
	} else {
		v, ok := rates[from]
		if !ok {
			return zero, fmt.Errorf("rate for %s not found", from)
		}
		eurToFrom = v
	}

	var eurToTo decimal.Decimal
	if to == "EUR" {
		eurToTo = decimal.NewFromInt(1)
	} else {
		v, ok := rates[to]
		if !ok {
			return zero, fmt.Errorf("rate for %s not found", to)
		}
		eurToTo = v
	}

	if eurToFrom.IsZero() {
		return zero, errors.New("base rate is zero")
	}

	return eurToTo.Div(eurToFrom), nil
}
