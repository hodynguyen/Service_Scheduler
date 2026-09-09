package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// bookingTx implements service.BookingTx over one pgx.Tx.
type bookingTx struct {
	tx pgx.Tx
}

var _ service.BookingTx = (*bookingTx)(nil)

func (b *bookingTx) DaySchedule(ctx context.Context, q service.ScheduleQuery) (domain.DaySchedule, error) {
	ctx, span := tracer.Start(ctx, "repository.DaySchedule")
	defer span.End()
	return daySchedule(ctx, b.tx, q)
}

func (b *bookingTx) Appointment(ctx context.Context, id string) (domain.Appointment, error) {
	return appointment(ctx, b.tx, id)
}

// InsertAppointment writes the row inside a savepoint so an exclusion
// violation leaves the outer transaction usable for a retry or for the
// idempotency record.
func (b *bookingTx) InsertAppointment(ctx context.Context, a service.NewAppointment) (domain.Appointment, error) {
	ctx, span := tracer.Start(ctx, "repository.InsertAppointment", trace.WithAttributes(
		attribute.String("technician.id", a.TechnicianID), attribute.String("bay.id", a.BayID)))
	defer span.End()

	sp, err := b.tx.Begin(ctx) // nested Begin == SAVEPOINT in pgx
	if err != nil {
		return domain.Appointment{}, fmt.Errorf("savepoint: %w", err)
	}
	var id string
	err = sp.QueryRow(ctx, `
		INSERT INTO appointment (dealership_id, vehicle_id, customer_id, service_type_id, required_skill_id, required_bay_type,
		                         technician_id, bay_id, start_time, end_time, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'CONFIRMED')
		RETURNING id`,
		a.DealershipID, a.VehicleID, a.CustomerID, a.ServiceTypeID, a.RequiredSkillID, string(a.RequiredBayType),
		a.TechnicianID, a.BayID, a.Interval.Start, a.Interval.End,
	).Scan(&id)
	if err != nil {
		_ = sp.Rollback(ctx)
		err = mapInsertError(err)
		if errors.Is(err, service.ErrLostRace) {
			span.SetAttributes(attribute.String("outcome", "lost_race"))
		} else {
			span.SetStatus(codes.Error, err.Error())
		}
		return domain.Appointment{}, err
	}
	if err := sp.Commit(ctx); err != nil {
		return domain.Appointment{}, fmt.Errorf("release savepoint: %w", err)
	}
	return appointment(ctx, b.tx, id)
}

// mapInsertError translates a database rejection caused by a concurrent
// booking into ErrLostRace wrapped around a domain error.
//
//   - 23P01 exclusion_violation: the competitor committed first; the
//     constraint name says which resource it took.
//   - 40P01 deadlock_detected: two inserts conflicted at the same instant and
//     each waited on the other's in-progress tuple during the exclusion check;
//     Postgres aborted this one. Nothing is known about the winner yet, so the
//     retry re-selects on fresh data.
func mapInsertError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("insert appointment: %w", err)
	}
	if pgErr.Code == "40P01" {
		return fmt.Errorf("%w: %w", service.ErrLostRace,
			domain.NewError(domain.CodeNoAvailableResource, "concurrent booking contention (deadlock resolved by the database)"))
	}
	if pgErr.Code == "23503" {
		// INV-4/INV-5/INV-7 foreign keys: an invariant the domain should have
		// upheld. Loud failure (500) is correct; it means a bug, not contention.
		return fmt.Errorf("insert appointment: database rejected an invariant violation (%s): %w", pgErr.ConstraintName, err)
	}
	if pgErr.Code != "23P01" {
		return fmt.Errorf("insert appointment: %w", err)
	}
	var de *domain.Error
	switch pgErr.ConstraintName {
	case ConstraintBayOverlap:
		de = &domain.Error{Code: domain.CodeNoAvailableResource, Message: "bay was booked concurrently", Conflicting: []domain.ResourceKind{domain.ResourceBay}}
	case ConstraintTechnicianOverlap:
		de = &domain.Error{Code: domain.CodeNoAvailableResource, Message: "technician was booked concurrently", Conflicting: []domain.ResourceKind{domain.ResourceTechnician}}
	case ConstraintVehicleOverlap:
		de = domain.NewError(domain.CodeVehicleAlreadyBooked, "vehicle was booked concurrently")
	default:
		return fmt.Errorf("insert appointment: unexpected exclusion constraint %q: %w", pgErr.ConstraintName, err)
	}
	return fmt.Errorf("%w: %w", service.ErrLostRace, de)
}

// LockIdempotencyKey takes a transaction-scoped advisory lock derived from
// (dealership, key). A hash collision only serialises unrelated requests.
func (b *bookingTx) LockIdempotencyKey(ctx context.Context, dealershipID, key string) error {
	_, err := b.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, dealershipID, key)
	if err != nil {
		return fmt.Errorf("lock idempotency key: %w", err)
	}
	return nil
}

// LockSchedulingDay takes a transaction-scoped advisory lock on (dealership,
// local date). Bookings for the same day then run one at a time through
// selection + insert, which turns N simultaneous inserts fighting over the
// exclusion constraints (deadlock cycles resolved 1 s at a time by Postgres)
// into N quick sequential decisions on committed data.
func (b *bookingTx) LockSchedulingDay(ctx context.Context, dealershipID string, day domain.Date) error {
	ctx, span := tracer.Start(ctx, "repository.LockSchedulingDay")
	defer span.End()
	_, err := b.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, dealershipID, "day:"+day.String())
	if err != nil {
		return fmt.Errorf("lock scheduling day: %w", err)
	}
	return nil
}

func (b *bookingTx) FindIdempotencyRecord(ctx context.Context, dealershipID, key string, now time.Time) (*service.IdempotencyRecord, error) {
	rec := &service.IdempotencyRecord{DealershipID: dealershipID, Key: key}
	var (
		apptID      *string
		errCode     *string
		conflicting []string
	)
	err := b.tx.QueryRow(ctx, `
		SELECT request_fingerprint, appointment_id, error_code, error_conflicting, expires_at
		FROM idempotency_key
		WHERE dealership_id = $1 AND key = $2 AND expires_at > $3`, dealershipID, key, now).
		Scan(&rec.Fingerprint, &apptID, &errCode, &conflicting, &rec.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select idempotency key: %w", err)
	}
	if apptID != nil {
		rec.AppointmentID = *apptID
	}
	if errCode != nil {
		rec.ErrorCode = domain.Code(*errCode)
	}
	for _, c := range conflicting {
		rec.Conflicting = append(rec.Conflicting, domain.ResourceKind(c))
	}
	return rec, nil
}

// SaveIdempotencyRecord upserts so an expired row can be reused.
func (b *bookingTx) SaveIdempotencyRecord(ctx context.Context, rec service.IdempotencyRecord) error {
	var (
		apptID      *string
		errCode     *string
		conflicting []string
		outcome     = "CREATED"
	)
	if rec.AppointmentID != "" {
		apptID = &rec.AppointmentID
	} else {
		outcome = "REJECTED"
		code := string(rec.ErrorCode)
		errCode = &code
		for _, c := range rec.Conflicting {
			conflicting = append(conflicting, string(c))
		}
	}
	_, err := b.tx.Exec(ctx, `
		INSERT INTO idempotency_key (dealership_id, key, request_fingerprint, outcome, appointment_id, error_code, error_conflicting, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (dealership_id, key) DO UPDATE SET
			request_fingerprint = EXCLUDED.request_fingerprint,
			outcome             = EXCLUDED.outcome,
			appointment_id      = EXCLUDED.appointment_id,
			error_code          = EXCLUDED.error_code,
			error_conflicting   = EXCLUDED.error_conflicting,
			created_at          = EXCLUDED.created_at,
			expires_at          = EXCLUDED.expires_at`,
		rec.DealershipID, rec.Key, rec.Fingerprint, outcome, apptID, errCode, conflicting,
		rec.ExpiresAt.Add(-service.IdempotencyRetention), rec.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}
