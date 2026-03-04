package service

import (
	"errors"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"

	"fxquotes/internal/domain"
)

func crossRate(from, to string, rates map[string]decimal.Decimal) (decimal.Decimal, error) {
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

func collectSymbols(pairs map[string]struct{}) []string {
	set := make(map[string]struct{})
	for raw := range pairs {
		p, err := domain.ParsePair(raw)
		if err != nil {
			continue
		}
		if p.From() != "EUR" {
			set[p.From()] = struct{}{}
		}
		if p.To() != "EUR" {
			set[p.To()] = struct{}{}
		}
	}
	symbols := make([]string, 0, len(set))
	for s := range set {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols
}
