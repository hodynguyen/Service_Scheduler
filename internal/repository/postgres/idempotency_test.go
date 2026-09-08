package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	. "github.com/hodynguyen/service-scheduler/internal/repository/postgres"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

const otherDealershipID = "10000000-0000-4000-8000-0000000000ff"

func withOtherDealership(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO dealership (id, name, timezone) VALUES ($1, 'Other', 'Europe/London') ON CONFLICT DO NOTHING`, otherDealershipID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM idempotency_key WHERE dealership_id = $1`, otherDealershipID)
		_, _ = pool.Exec(ctx, `DELETE FROM dealership WHERE id = $1`, otherDealershipID)
	})
}

func TestIdempotency_AC19_KeyIsScopedPerDealership(t *testing.T) {
	pool := requireDB(t)
	withOtherDealership(t, pool)
	repo := New(pool)
	ctx := context.Background()
	now := time.Date(2030, time.March, 4, 2, 0, 0, 0, time.UTC)
	key := "8d2c8a52-1b1e-4c65-9b26-0000000000d1"

	err := repo.InTx(ctx, func(tx service.BookingTx) error {
		return tx.SaveIdempotencyRecord(ctx, service.IdempotencyRecord{
			DealershipID: SeedDealershipID, Key: key, Fingerprint: "fp",
			ErrorCode: domain.CodeNoAvailableResource, Conflicting: []domain.ResourceKind{domain.ResourceBay},
			ExpiresAt: now.Add(service.IdempotencyRetention),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.InTx(ctx, func(tx service.BookingTx) error {
		same, err := tx.FindIdempotencyRecord(ctx, SeedDealershipID, key, now)
		if err != nil || same == nil || same.ErrorCode != domain.CodeNoAvailableResource || len(same.Conflicting) != 1 {
			t.Fatalf("record for the owning dealership must be found: %+v %v", same, err)
		}
		other, err := tx.FindIdempotencyRecord(ctx, otherDealershipID, key, now)
		if err != nil || other != nil {
			t.Fatalf("the same key in another dealership must be unknown (FR-4 scope), got %+v %v", other, err)
		}
		return nil
	})
}

func TestIdempotency_FR4_RecordsExpireAfter24HoursAndCanBeReused(t *testing.T) {
	pool := requireDB(t)
	repo := New(pool)
	ctx := context.Background()
	created := time.Date(2030, time.March, 4, 2, 0, 0, 0, time.UTC)
	key := "8d2c8a52-1b1e-4c65-9b26-0000000000d2"
	rec := service.IdempotencyRecord{DealershipID: SeedDealershipID, Key: key, Fingerprint: "fp-1",
		ErrorCode: domain.CodeVehicleAlreadyBooked, ExpiresAt: created.Add(service.IdempotencyRetention)}
	if err := repo.InTx(ctx, func(tx service.BookingTx) error { return tx.SaveIdempotencyRecord(ctx, rec) }); err != nil {
		t.Fatal(err)
	}
	_ = repo.InTx(ctx, func(tx service.BookingTx) error {
		if r, _ := tx.FindIdempotencyRecord(ctx, SeedDealershipID, key, created.Add(23*time.Hour)); r == nil {
			t.Fatal("record must be visible within 24 h")
		}
		if r, _ := tx.FindIdempotencyRecord(ctx, SeedDealershipID, key, created.Add(24*time.Hour)); r != nil {
			t.Fatalf("record must be invisible once expires_at is reached, got %+v", r)
		}
		// An expired row can be overwritten by a new outcome under the same key.
		fresh := rec
		fresh.Fingerprint, fresh.ExpiresAt = "fp-2", created.Add(48*time.Hour)
		if err := tx.SaveIdempotencyRecord(ctx, fresh); err != nil {
			t.Fatalf("upsert over an expired key must succeed: %v", err)
		}
		r, _ := tx.FindIdempotencyRecord(ctx, SeedDealershipID, key, created.Add(25*time.Hour))
		if r == nil || r.Fingerprint != "fp-2" {
			t.Fatalf("reused key must carry the new outcome, got %+v", r)
		}
		return nil
	})
}
