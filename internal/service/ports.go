package service

import (
	"context"
	"errors"
	"time"

	"github.com/hodynguyen/service-scheduler/internal/domain"
)

// Repository is the persistence port consumed by the service. It is defined
// here (consumer side) and implemented by internal/repository/postgres.
type Repository interface {
	Dealership(ctx context.Context, id string) (domain.Dealership, error)
	ServiceType(ctx context.Context, id string) (domain.ServiceType, error)
	// VehicleWithOwner is scoped to the dealership (BR-7): a vehicle registered
	// elsewhere is reported as not found.
	VehicleWithOwner(ctx context.Context, dealershipID, vehicleID string) (domain.Vehicle, domain.Customer, error)
	Appointment(ctx context.Context, id string) (domain.Appointment, error)
	DaySchedule(ctx context.Context, q ScheduleQuery) (domain.DaySchedule, error)
	// InTx runs fn inside one database transaction. A nil return commits;
	// any error rolls back.
	InTx(ctx context.Context, fn func(tx BookingTx) error) error
}

// BookingTx is the transaction-scoped slice of the repository used by the
// booking use case.
type BookingTx interface {
	DaySchedule(ctx context.Context, q ScheduleQuery) (domain.DaySchedule, error)
	// InsertAppointment writes one CONFIRMED appointment inside a savepoint.
	// When the database rejects it through an exclusion constraint the error
	// wraps both ErrLostRace and the *domain.Error describing the resource, and
	// the enclosing transaction remains usable.
	InsertAppointment(ctx context.Context, a NewAppointment) (domain.Appointment, error)
	Appointment(ctx context.Context, id string) (domain.Appointment, error)

	// LockIdempotencyKey serialises concurrent requests carrying the same key
	// for the rest of the transaction.
	LockIdempotencyKey(ctx context.Context, dealershipID, key string) error
	FindIdempotencyRecord(ctx context.Context, dealershipID, key string, now time.Time) (*IdempotencyRecord, error)
	SaveIdempotencyRecord(ctx context.Context, rec IdempotencyRecord) error
}

// ScheduleQuery selects one dealership's CONFIRMED appointments whose start
// lies within Day, plus (optionally) those of one vehicle.
type ScheduleQuery struct {
	DealershipID string
	Day          domain.Interval
	VehicleID    string
}

// NewAppointment is the write model for a booking.
type NewAppointment struct {
	DealershipID  string
	VehicleID     string
	CustomerID    string
	ServiceTypeID string
	TechnicianID  string
	BayID         string
	Interval      domain.Interval
}

// IdempotencyRecord is the stored outcome of a keyed booking request (FR-4).
// Exactly one of AppointmentID or ErrorCode is set.
type IdempotencyRecord struct {
	DealershipID  string
	Key           string
	Fingerprint   string
	AppointmentID string
	ErrorCode     domain.Code
	Conflicting   []domain.ResourceKind
	ExpiresAt     time.Time
}

// ErrLostRace marks a database rejection caused by a concurrent booking that
// committed first. The service retries selection on fresh data.
var ErrLostRace = errors.New("lost race for a resource")
