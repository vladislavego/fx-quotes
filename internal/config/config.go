// Package config loads application configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all application settings.
type Config struct {
	DatabaseURL       string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	FXAPIURL          string
	FXAPIKey          string
	FXAPITimeout      time.Duration
	HTTPAddr          string
	WorkerCount       int
	RetryMaxAttempts  int
	RetryBaseDelay    time.Duration
	PollInterval      time.Duration
	BatchSize         int
	JobTimeout        time.Duration
	MaxClaimAttempts  int
	StaleAfter        time.Duration
}

// Load reads configuration from environment variables and validates it.
func Load() (Config, error) {
	workerCount, err := getenvInt("WORKER_COUNT", 1)
	if err != nil {
		return Config{}, err
	}
	retryMaxAttempts, err := getenvInt("RETRY_MAX_ATTEMPTS", 3)
	if err != nil {
		return Config{}, err
	}
	retryBaseDelayMs, err := getenvInt("RETRY_BASE_DELAY_MS", 500)
	if err != nil {
		return Config{}, err
	}
	pollIntervalMs, err := getenvInt("POLL_INTERVAL_MS", 1000)
	if err != nil {
		return Config{}, err
	}
	batchSize, err := getenvInt("BATCH_SIZE", 10)
	if err != nil {
		return Config{}, err
	}
	jobTimeoutMs, err := getenvInt("JOB_TIMEOUT_MS", 30000)
	if err != nil {
		return Config{}, err
	}
	maxClaimAttempts, err := getenvInt("MAX_CLAIM_ATTEMPTS", 5)
	if err != nil {
		return Config{}, err
	}
	fxAPITimeoutMs, err := getenvInt("FX_API_TIMEOUT_MS", 5000)
	if err != nil {
		return Config{}, err
	}
	staleAfterMs, err := getenvInt("STALE_AFTER_MS", 0)
	if err != nil {
		return Config{}, err
	}
	dbMaxOpenConns, err := getenvInt("DB_MAX_OPEN_CONNS", 25)
	if err != nil {
		return Config{}, err
	}
	dbMaxIdleConns, err := getenvInt("DB_MAX_IDLE_CONNS", 5)
	if err != nil {
		return Config{}, err
	}
	dbConnMaxLifetimeS, err := getenvInt("DB_CONN_MAX_LIFETIME_S", 300)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		DatabaseURL:       getenv("DATABASE_URL", "postgres://user:pass@localhost:5432/quotes?sslmode=disable"),
		FXAPIURL:          getenv("FX_API_URL", "https://api.exchangeratesapi.io/v1"),
		FXAPIKey:          os.Getenv("FX_API_KEY"),
		FXAPITimeout:      time.Duration(fxAPITimeoutMs) * time.Millisecond,
		HTTPAddr:          getenv("HTTP_ADDR", ":8080"),
		DBMaxOpenConns:    dbMaxOpenConns,
		DBMaxIdleConns:    dbMaxIdleConns,
		DBConnMaxLifetime: time.Duration(dbConnMaxLifetimeS) * time.Second,
		WorkerCount:       workerCount,
		RetryMaxAttempts:  retryMaxAttempts,
		RetryBaseDelay:    time.Duration(retryBaseDelayMs) * time.Millisecond,
		PollInterval:      time.Duration(pollIntervalMs) * time.Millisecond,
		BatchSize:         batchSize,
		JobTimeout:        time.Duration(jobTimeoutMs) * time.Millisecond,
		MaxClaimAttempts:  maxClaimAttempts,
		StaleAfter:        time.Duration(staleAfterMs) * time.Millisecond,
	}

	if cfg.StaleAfter == 0 {
		cfg.StaleAfter = 2 * cfg.JobTimeout
	}

	if cfg.DBMaxOpenConns < 1 {
		return Config{}, fmt.Errorf("DB_MAX_OPEN_CONNS must be >= 1, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns < 0 {
		return Config{}, fmt.Errorf("DB_MAX_IDLE_CONNS must be >= 0, got %d", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime < 0 {
		return Config{}, fmt.Errorf("DB_CONN_MAX_LIFETIME_S must be >= 0, got %d", dbConnMaxLifetimeS)
	}
	if cfg.FXAPITimeout < time.Second {
		return Config{}, fmt.Errorf("FX_API_TIMEOUT_MS must be >= 1000, got %d", fxAPITimeoutMs)
	}
	if cfg.FXAPIKey == "" {
		return Config{}, errors.New("FX_API_KEY is required")
	}
	if cfg.WorkerCount < 1 {
		return Config{}, fmt.Errorf("WORKER_COUNT must be >= 1, got %d", cfg.WorkerCount)
	}
	if cfg.RetryMaxAttempts < 1 {
		return Config{}, fmt.Errorf("RETRY_MAX_ATTEMPTS must be >= 1, got %d", cfg.RetryMaxAttempts)
	}
	if cfg.RetryBaseDelay < 0 {
		return Config{}, fmt.Errorf("RETRY_BASE_DELAY_MS must be >= 0, got %d", retryBaseDelayMs)
	}
	if cfg.PollInterval < 100*time.Millisecond {
		return Config{}, fmt.Errorf("POLL_INTERVAL_MS must be >= 100, got %d", pollIntervalMs)
	}
	if cfg.BatchSize < 1 {
		return Config{}, fmt.Errorf("BATCH_SIZE must be >= 1, got %d", cfg.BatchSize)
	}
	if cfg.JobTimeout < time.Second {
		return Config{}, fmt.Errorf("JOB_TIMEOUT_MS must be >= 1000, got %d", jobTimeoutMs)
	}
	if cfg.MaxClaimAttempts < 1 {
		return Config{}, fmt.Errorf("MAX_CLAIM_ATTEMPTS must be >= 1, got %d", cfg.MaxClaimAttempts)
	}
	if cfg.StaleAfter < time.Second {
		return Config{}, fmt.Errorf("STALE_AFTER must be >= 1s, got %v", cfg.StaleAfter)
	}

	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", key, v)
	}
	return i, nil
}
