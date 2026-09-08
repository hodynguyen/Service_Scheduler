package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hodynguyen/service-scheduler/internal/testutil/pgtest"
)

// requireDB returns the shared container-backed pool with mutable tables
// truncated. Skips under -short.
func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.Reset(t, pool)
	return pool
}
