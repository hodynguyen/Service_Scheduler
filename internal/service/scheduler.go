package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/hodynguyen/service-scheduler/internal/domain"
)

// IdempotencyRetention is how long a processed key is remembered (FR-4).
const IdempotencyRetention = 24 * time.Hour

// Scheduler implements FR-1..FR-4.
type Scheduler struct {
	repo        Repository
	policy      domain.AssignmentPolicy
	now         func() time.Time
	granularity time.Duration
	maxAttempts int
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithClock overrides the time source (tests).
func WithClock(now func() time.Time) Option { return func(s *Scheduler) { s.now = now } }

// WithPolicy swaps the assignment policy (A-4).
func WithPolicy(p domain.AssignmentPolicy) Option { return func(s *Scheduler) { s.policy = p } }

// WithSlotGranularity changes the availability step (FR-1).
func WithSlotGranularity(d time.Duration) Option { return func(s *Scheduler) { s.granularity = d } }

// WithMaxAttempts bounds how often a booking re-selects after losing a race.
func WithMaxAttempts(n int) Option { return func(s *Scheduler) { s.maxAttempts = n } }

// New builds a Scheduler with the least-loaded policy and wall-clock time.
func New(repo Repository, opts ...Option) *Scheduler {
	s := &Scheduler{
		repo:        repo,
		policy:      domain.LeastLoadedPolicy{},
		now:         time.Now,
		granularity: domain.DefaultSlotGranularity,
		maxAttempts: 3,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// AvailabilityQuery is the input of FR-1. VehicleID is optional; when set,
// INV-3 is evaluated as well.
type AvailabilityQuery struct {
	DealershipID  string
	ServiceTypeID string
	Date          string // YYYY-MM-DD in the dealership's calendar
	VehicleID     string
}

// AvailabilityResult is the output of FR-1.
type AvailabilityResult struct {
	Date        domain.Date
	ServiceType domain.ServiceType
	Slots       []time.Time // in the dealership's location, never nil
}

// Availability returns the bookable start times for a service type on a date.
func (s *Scheduler) Availability(ctx context.Context, q AvailabilityQuery) (AvailabilityResult, error) {
	date, err := domain.ParseDate(q.Date)
	if err != nil {
		return AvailabilityResult{}, domain.NewError(domain.CodeValidationError, "%v", err)
	}
	dealership, err := s.repo.Dealership(ctx, q.DealershipID)
	if err != nil {
		return AvailabilityResult{}, err
	}
	st, err := s.repo.ServiceType(ctx, q.ServiceTypeID)
	if err != nil {
		return AvailabilityResult{}, err
	}
	if q.VehicleID != "" {
		if _, _, err := s.repo.VehicleWithOwner(ctx, q.DealershipID, q.VehicleID); err != nil {
			return AvailabilityResult{}, err
		}
	}
	result := AvailabilityResult{Date: date, ServiceType: st, Slots: []time.Time{}}
	window, open := dealership.Hours.Window(date, dealership.Location)
	if !open {
		return result, nil
	}
	schedule, err := s.repo.DaySchedule(ctx, ScheduleQuery{
		DealershipID: q.DealershipID,
		Day:          date.Bounds(dealership.Location),
		VehicleID:    q.VehicleID,
	})
	if err != nil {
		return AvailabilityResult{}, err
	}
	result.Slots = domain.AvailableSlots(window, st, schedule, s.now(), s.granularity, s.policy)
	return result, nil
}

// BookRequest is the input of FR-2. IdempotencyKey is optional (FR-4).
type BookRequest struct {
	DealershipID   string
	VehicleID      string
	ServiceTypeID  string
	StartTime      time.Time
	IdempotencyKey string
}

// fingerprint identifies the payload so a replayed key with a different body
// can be detected.
func (r BookRequest) fingerprint() string {
	sum := sha256.Sum256([]byte(r.DealershipID + "|" + r.VehicleID + "|" + r.ServiceTypeID + "|" + r.StartTime.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}

// Book creates a CONFIRMED appointment or returns a *domain.Error explaining
// why not. Reference lookups and time validation happen before the
// transaction; candidate selection, the insert and the idempotency record
// happen inside it.
func (s *Scheduler) Book(ctx context.Context, req BookRequest) (domain.Appointment, error) {
	dealership, err := s.repo.Dealership(ctx, req.DealershipID)
	if err != nil {
		return domain.Appointment{}, err
	}
	st, err := s.repo.ServiceType(ctx, req.ServiceTypeID)
	if err != nil {
		return domain.Appointment{}, err
	}
	vehicle, customer, err := s.repo.VehicleWithOwner(ctx, req.DealershipID, req.VehicleID)
	if err != nil {
		return domain.Appointment{}, err
	}
	now := s.now()
	iv, err := domain.ValidateBookingTime(req.StartTime, st.Duration, now, dealership.Location, dealership.Hours)
	if err != nil {
		return domain.Appointment{}, err
	}
	query := ScheduleQuery{
		DealershipID: req.DealershipID,
		Day:          domain.DateOf(iv.Start, dealership.Location).Bounds(dealership.Location),
		VehicleID:    vehicle.ID,
	}
	newAppt := NewAppointment{
		DealershipID:  req.DealershipID,
		VehicleID:     vehicle.ID,
		CustomerID:    customer.ID,
		ServiceTypeID: st.ID,
		Interval:      iv,
	}

	var (
		appt    domain.Appointment
		outcome error // a *domain.Error rejection that must still commit the transaction
	)
	err = s.repo.InTx(ctx, func(tx BookingTx) error {
		if req.IdempotencyKey != "" {
			if err := tx.LockIdempotencyKey(ctx, req.DealershipID, req.IdempotencyKey); err != nil {
				return err
			}
			rec, err := tx.FindIdempotencyRecord(ctx, req.DealershipID, req.IdempotencyKey, now)
			if err != nil {
				return err
			}
			if rec != nil {
				appt, outcome, err = s.replay(ctx, tx, rec, req.fingerprint())
				return err
			}
		}

		appt, outcome = s.assignAndInsert(ctx, tx, query, st, newAppt)
		if outcome != nil {
			if _, ok := domain.AsError(outcome); !ok {
				return outcome // infrastructure failure: roll everything back
			}
		}
		if req.IdempotencyKey != "" {
			rec := IdempotencyRecord{
				DealershipID: req.DealershipID,
				Key:          req.IdempotencyKey,
				Fingerprint:  req.fingerprint(),
				ExpiresAt:    now.Add(IdempotencyRetention),
			}
			if outcome == nil {
				rec.AppointmentID = appt.ID
			} else {
				de, _ := domain.AsError(outcome)
				rec.ErrorCode, rec.Conflicting = de.Code, de.Conflicting
			}
			if err := tx.SaveIdempotencyRecord(ctx, rec); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Appointment{}, err
	}
	if outcome != nil {
		return domain.Appointment{}, outcome
	}
	return appt, nil
}

// replay reproduces the stored outcome of a previously processed key.
func (s *Scheduler) replay(ctx context.Context, tx BookingTx, rec *IdempotencyRecord, fingerprint string) (domain.Appointment, error, error) {
	if rec.Fingerprint != fingerprint {
		return domain.Appointment{}, domain.NewError(domain.CodeIdempotencyKeyReused,
			"Idempotency-Key %q was already used with a different request body", rec.Key), nil
	}
	if rec.AppointmentID != "" {
		appt, err := tx.Appointment(ctx, rec.AppointmentID)
		if err != nil {
			return domain.Appointment{}, nil, fmt.Errorf("replay appointment %s: %w", rec.AppointmentID, err)
		}
		return appt, nil, nil
	}
	return domain.Appointment{}, &domain.Error{
		Code:        rec.ErrorCode,
		Message:     "replayed result of a previous request with the same Idempotency-Key",
		Conflicting: rec.Conflicting,
	}, nil
}

// assignAndInsert selects resources on fresh data and lets the database
// adjudicate. Losing a race to a concurrent booking is not a rejection by
// itself: the selection is repeated (bounded) so spare resources are used.
// When no candidate remains, Assign produces the precise NO_AVAILABLE_RESOURCE
// / VEHICLE_ALREADY_BOOKED answer.
func (s *Scheduler) assignAndInsert(ctx context.Context, tx BookingTx, q ScheduleQuery, st domain.ServiceType, na NewAppointment) (domain.Appointment, error) {
	var lastRace error
	for attempt := 0; attempt < s.maxAttempts; attempt++ {
		schedule, err := tx.DaySchedule(ctx, q)
		if err != nil {
			return domain.Appointment{}, err
		}
		assignment, err := domain.Assign(schedule, st, na.Interval, s.policy)
		if err != nil {
			return domain.Appointment{}, err
		}
		na.TechnicianID, na.BayID = assignment.Technician.ID, assignment.Bay.ID
		appt, err := tx.InsertAppointment(ctx, na)
		if err == nil {
			return appt, nil
		}
		if !errors.Is(err, ErrLostRace) {
			return domain.Appointment{}, err
		}
		lastRace = err
	}
	// Attempts exhausted under sustained contention: report the database's last verdict.
	if de, ok := domain.AsError(lastRace); ok {
		return domain.Appointment{}, de
	}
	return domain.Appointment{}, lastRace
}

// GetAppointment implements FR-3.
func (s *Scheduler) GetAppointment(ctx context.Context, id string) (domain.Appointment, error) {
	return s.repo.Appointment(ctx, id)
}
