// Package main is the entry point for the FX quotes service.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"fxquotes/internal/config"
	"fxquotes/internal/fxclient"
	"fxquotes/internal/httpserver"
	"fxquotes/internal/repository"
	"fxquotes/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	repo := repository.NewPostgresQuoteRepository(db)
	fx := fxclient.NewHTTPClient(cfg.FXAPIURL, cfg.FXAPIKey, cfg.FXAPITimeout)

	svc := service.NewQuoteService(repo)
	worker := service.NewWorker(repo, fx, logger, service.WorkerConfig{
		RetryMaxAttempts: cfg.RetryMaxAttempts,
		RetryBaseDelay:   cfg.RetryBaseDelay,
		PollInterval:     cfg.PollInterval,
		JobTimeout:       cfg.JobTimeout,
		BatchSize:        cfg.BatchSize,
		MaxClaimAttempts: cfg.MaxClaimAttempts,
		StaleAfter:       cfg.StaleAfter,
	})

	server := httpserver.NewServer(svc, db, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.HandleHealth)
	mux.HandleFunc("POST /api/v1/quote-updates", server.HandleUpdateQuote)
	mux.HandleFunc("GET /api/v1/quote-updates/latest", server.HandleGetLatest)
	mux.HandleFunc("GET /api/v1/quote-updates/{id}", server.HandleGetUpdate)

	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	for i := range cfg.WorkerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("starting worker", "worker_id", i)
			worker.Run(ctx)
		}()
	}

	go func() {
		logger.Info("starting http server", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}

	wg.Wait()

	logger.Info("server stopped")
	return nil
}
