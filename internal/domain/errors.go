// Package domain defines shared types, sentinel errors, and core business logic.
package domain

import "errors"

// Sentinel errors used across layers.
var (
	ErrNotFound              = errors.New("not found")
	ErrUnsupportedPair       = errors.New("unsupported pair")
	ErrInvalidPair           = errors.New("invalid pair format")
	ErrInvalidRequestID      = errors.New("invalid request_id")
	ErrRequestIDPairMismatch = errors.New("request_id already used for a different pair")
)
