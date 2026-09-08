package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	. "github.com/hodynguyen/service-scheduler/internal/repository/postgres"
	"github.com/hodynguyen/service-scheduler/internal/testutil/pgtest"
)

// These tests exercise the schema directly with SQL. They prove that the
// database — not application code — is what guarantees INV-1..INV-3 and
// INV-7, before any repository or service code exists.

func TestSchema_MigrateIsIdempotent(t *testing.T) {
	pool := requireDB(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second Migrate run must be a no-op, got: %v", err)
	}
	if err := Seed(context.Background(), pool); err != nil {
		t.Fatalf("second Seed run must be a no-op, got: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM technician`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("seed applied twice must not duplicate rows: technicians = %d, want 4", n)
	}
}

func TestSchema_INV1to3_ExclusionConstraintsArePartialOnConfirmed(t *testing.T) {
	pool := requireDB(t)
	rows, err := pool.Query(context.Background(), `
		SELECT conname, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = 'appointment'::regclass AND contype = 'x'
		ORDER BY conname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		got[name] = def
	}
	want := map[string]string{
		ConstraintBayOverlap:        "bay_id WITH =",
		ConstraintTechnicianOverlap: "technician_id WITH =",
		ConstraintVehicleOverlap:    "vehicle_id WITH =",
	}
	if len(got) != len(want) {
		t.Fatalf("exclusion constraints = %v, want exactly %d", got, len(want))
	}
	for name, column := range want {
		def, ok := got[name]
		if !ok {
			t.Errorf("missing exclusion constraint %q", name)
			continue
		}
		for _, frag := range []string{"USING gist", column, "tstzrange(start_time, end_time, '[)'", "status = 'CONFIRMED'"} {
			if !strings.Contains(def, frag) {
				t.Errorf("%s: definition %q lacks %q", name, def, frag)
			}
		}
	}
}

func insertAppointment(t *testing.T, vehicle, tech, bay string, start, end time.Time, status string) (string, error) {
	t.Helper()
	var id string
	err := pgtest.Pool(t).QueryRow(context.Background(), `
		INSERT INTO appointment (dealership_id, vehicle_id, customer_id, service_type_id, technician_id, bay_id, start_time, end_time, status)
		VALUES ($1, $2, (SELECT customer_id FROM vehicle WHERE id = $2), $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		SeedDealershipID, vehicle, SeedServiceTypeOilChangeID, tech, bay, start, end, status).Scan(&id)
	return id, err
}

func exclusionViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected PgError, got %v", err)
	}
	if pgErr.Code != "23P01" {
		t.Fatalf("SQLSTATE = %s, want 23P01 (exclusion_violation): %v", pgErr.Code, err)
	}
	if pgErr.ConstraintName != constraint {
		t.Fatalf("constraint = %s, want %s", pgErr.ConstraintName, constraint)
	}
}

var (
	day   = time.Date(2030, time.March, 4, 2, 0, 0, 0, time.UTC) // Monday 09:00 Asia/Ho_Chi_Minh
	t0900 = day
	t0930 = day.Add(30 * time.Minute)
	t1000 = day.Add(60 * time.Minute)
	t1030 = day.Add(90 * time.Minute)
)

func TestSchema_INV1_BayExclusionRejectsOverlap(t *testing.T) {
	requireDB(t)
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t0900, t1000, "CONFIRMED"); err != nil {
		t.Fatal(err)
	}
	_, err := insertAppointment(t, SeedVehicleCRVID, SeedTechnicianBinhID, SeedBay1ID, t0930, t1030, "CONFIRMED")
	exclusionViolation(t, err, ConstraintBayOverlap)
}

func TestSchema_INV2_TechnicianExclusionRejectsOverlap(t *testing.T) {
	requireDB(t)
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t0900, t1000, "CONFIRMED"); err != nil {
		t.Fatal(err)
	}
	_, err := insertAppointment(t, SeedVehicleCRVID, SeedTechnicianAnID, SeedBay2ID, t0930, t1030, "CONFIRMED")
	exclusionViolation(t, err, ConstraintTechnicianOverlap)
}

func TestSchema_INV3_VehicleExclusionRejectsOverlap(t *testing.T) {
	requireDB(t)
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t0900, t1000, "CONFIRMED"); err != nil {
		t.Fatal(err)
	}
	_, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianBinhID, SeedBay2ID, t0930, t1030, "CONFIRMED")
	exclusionViolation(t, err, ConstraintVehicleOverlap)
}

func TestSchema_AC10_AC11_AdjacentIntervalsDoNotConflict(t *testing.T) {
	requireDB(t)
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t0900, t1000, "CONFIRMED"); err != nil {
		t.Fatal(err)
	}
	// Same bay, technician and vehicle, starting exactly when the first ends: half-open [start, end).
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t1000, t1030, "CONFIRMED"); err != nil {
		t.Fatalf("adjacent interval must be accepted (half-open ranges): %v", err)
	}
}

func TestSchema_AC17_CancelledAppointmentDoesNotBlock(t *testing.T) {
	requireDB(t)
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t0900, t1000, "CANCELLED"); err != nil {
		t.Fatal(err)
	}
	if _, err := insertAppointment(t, SeedVehicleCamryID, SeedTechnicianAnID, SeedBay1ID, t0900, t1000, "CONFIRMED"); err != nil {
		t.Fatalf("cancelled appointment must release its resources (BR-9): %v", err)
	}
}

func TestSchema_INV7_ResourcesMustBelongToAppointmentDealership(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	var otherDealership, otherTech string
	if err := pool.QueryRow(ctx, `INSERT INTO dealership (name, timezone) VALUES ('Other', 'Europe/London') RETURNING id`).Scan(&otherDealership); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM dealership WHERE id = $1`, otherDealership) })
	if err := pool.QueryRow(ctx, `INSERT INTO technician (dealership_id, name) VALUES ($1, 'Stranger') RETURNING id`, otherDealership).Scan(&otherTech); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM technician WHERE id = $1`, otherTech) })

	var otherBay, otherVehicle string
	if err := pool.QueryRow(ctx, `INSERT INTO service_bay (dealership_id, name, bay_type) VALUES ($1, 'Far bay', 'GENERAL') RETURNING id`, otherDealership).Scan(&otherBay); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO vehicle (dealership_id, customer_id, vin, model) VALUES ($1, $2, 'FARVIN00000000001', 'Mini') RETURNING id`, otherDealership, SeedCustomerMaiID).Scan(&otherVehicle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM vehicle WHERE id = $1`, otherVehicle)
		_, _ = pool.Exec(ctx, `DELETE FROM service_bay WHERE id = $1`, otherBay)
	})

	cases := map[string]struct{ vehicle, tech, bay string }{
		"technician": {SeedVehicleCamryID, otherTech, SeedBay1ID},
		"bay":        {SeedVehicleCamryID, SeedTechnicianAnID, otherBay},
		"vehicle":    {otherVehicle, SeedTechnicianAnID, SeedBay1ID},
	}
	for name, c := range cases {
		_, err := insertAppointment(t, c.vehicle, c.tech, c.bay, t0900, t1000, "CONFIRMED")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("%s from another dealership must violate a foreign key (INV-7), got %v", name, err)
		}
	}
}
