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
		INSERT INTO appointment (dealership_id, vehicle_id, customer_id, service_type_id, technician_id, bay_id, start_time, end_time, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'CONFIRMED')
		RETURNING id`,
		a.DealershipID, a.VehicleID, a.CustomerID, a.ServiceTypeID, a.TechnicianID, a.BayID, a.Interval.Start, a.Interval.End,
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

// mapInsertError translates an exclusion violation into ErrLostRace wrapped
// around the matching domain error. Which constraint fired tells us which
// resource was taken by a concurrent transaction.
func mapInsertError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23P01" {
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8)
		ON CONFLICT (dealership_id, key) DO UPDATE SET
			request_fingerprint = EXCLUDED.request_fingerprint,
			outcome             = EXCLUDED.outcome,
			appointment_id      = EXCLUDED.appointment_id,
			error_code          = EXCLUDED.error_code,
			error_conflicting   = EXCLUDED.error_conflicting,
			created_at          = now(),
			expires_at          = EXCLUDED.expires_at`,
		rec.DealershipID, rec.Key, rec.Fingerprint, outcome, apptID, errCode, conflicting, rec.ExpiresAt)
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}
