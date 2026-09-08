// Command server runs the Unified Service Scheduler HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // dealership IANA zones must resolve even on images without zoneinfo

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hodynguyen/service-scheduler/internal/config"
	"github.com/hodynguyen/service-scheduler/internal/httpapi"
	"github.com/hodynguyen/service-scheduler/internal/observability"
	"github.com/hodynguyen/service-scheduler/internal/repository/postgres"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, parseLevel(cfg.LogLevel))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := observability.SetupTracing(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(flushCtx)
	}()
	metrics := observability.NewMetrics(nil)

	pool, err := connect(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if cfg.Seed {
		if err := postgres.Seed(ctx, pool); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		logger.Info("reference data seeded", "dealership_id", postgres.SeedDealershipID)
	}

	scheduler := service.New(postgres.New(pool), service.WithInstrumentation(metrics))
	router := httpapi.NewRouter(scheduler,
		httpapi.WithMiddleware(
			observability.CorrelationMiddleware,
			observability.TracingMiddleware,
			metrics.HTTPMiddleware,
			observability.RequestLogger(logger),
		),
		httpapi.WithRoutes(func(r chi.Router) { r.Method(http.MethodGet, "/metrics", metrics.Handler()) }),
		httpapi.WithReadiness(pool.Ping),
	)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// connect retries until the database accepts connections (compose starts the
// app only after the healthcheck, but a restart may still race it).
func connect(ctx context.Context, url string, logger *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("database not reachable: %w", err)
		}
		logger.Warn("database not ready, retrying", "error", err)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		}
	}
}

func parseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}
