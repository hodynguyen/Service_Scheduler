// Package pgtest starts one migrated and seeded PostgreSQL 16 container per
// test binary (testcontainers-go) and hands out a shared pool. Containers
// are reaped by testcontainers' Ryuk sidecar when the process exits.
package pgtest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/hodynguyen/service-scheduler/internal/repository/postgres"
)

var (
	once sync.Once
	pool *pgxpool.Pool
	err  error
)

// Pool returns the shared pool, starting the container on first use. It
// skips the calling test under -short so unit runs never need Docker.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: requires Docker (run without -short)")
	}
	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		var container *tcpostgres.PostgresContainer
		container, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("scheduler"),
			tcpostgres.WithUsername("scheduler"),
			tcpostgres.WithPassword("scheduler"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			return
		}
		var dsn string
		if dsn, err = container.ConnectionString(ctx, "sslmode=disable"); err != nil {
			return
		}
		var cfg *pgxpool.Config
		if cfg, err = pgxpool.ParseConfig(dsn); err != nil {
			return
		}
		cfg.MaxConns = 32 // concurrency tests hold many transactions open at once
		if pool, err = pgxpool.NewWithConfig(ctx, cfg); err != nil {
			return
		}
		if err = postgres.Migrate(ctx, pool); err != nil {
			return
		}
		err = postgres.Seed(ctx, pool)
	})
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	return pool
}

// Reset truncates the mutable tables so a test starts from seed data only.
func Reset(t testing.TB, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(context.Background(), `TRUNCATE appointment, idempotency_key`); err != nil {
		t.Fatalf("pgtest reset: %v", err)
	}
}
