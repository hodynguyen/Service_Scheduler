package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// Repository implements service.Repository on top of pgx. All SQL in this
// package is data access only: filtering by qualification, overlap and load
// happens in the domain.
type Repository struct {
	pool *pgxpool.Pool
}

// New wraps a pool.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ service.Repository = (*Repository)(nil)

// querier is satisfied by *pgxpool.Pool and pgx.Tx.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID guards lookups so a malformed identifier reads as "not found"
// instead of a driver encoding error.
func isUUID(s string) bool { return uuidRe.MatchString(s) }

// Dealership loads a dealership with its timezone and weekly hours.
func (r *Repository) Dealership(ctx context.Context, id string) (domain.Dealership, error) {
	return dealership(ctx, r.pool, id)
}

func dealership(ctx context.Context, q querier, id string) (domain.Dealership, error) {
	if !isUUID(id) {
		return domain.Dealership{}, domain.NotFound("dealership", id)
	}
	var d domain.Dealership
	var tz string
	err := q.QueryRow(ctx, `SELECT id, name, timezone FROM dealership WHERE id = $1`, id).Scan(&d.ID, &d.Name, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Dealership{}, domain.NotFound("dealership", id)
	}
	if err != nil {
		return domain.Dealership{}, fmt.Errorf("select dealership: %w", err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return domain.Dealership{}, fmt.Errorf("dealership %s has invalid timezone %q: %w", id, tz, err)
	}
	d.Location = loc

	rows, err := q.Query(ctx, `SELECT weekday, opens_at, closes_at FROM business_hours WHERE dealership_id = $1`, id)
	if err != nil {
		return domain.Dealership{}, fmt.Errorf("select business hours: %w", err)
	}
	defer rows.Close()
	d.Hours = domain.BusinessHours{}
	for rows.Next() {
		var weekday int16
		var opens, closes pgtype.Time
		if err := rows.Scan(&weekday, &opens, &closes); err != nil {
			return domain.Dealership{}, fmt.Errorf("scan business hours: %w", err)
		}
		d.Hours[time.Weekday(weekday)] = domain.OpeningHours{
			OpensAt:  int(opens.Microseconds / int64(time.Minute/time.Microsecond)),
			ClosesAt: int(closes.Microseconds / int64(time.Minute/time.Microsecond)),
		}
	}
	return d, rows.Err()
}

// ServiceType loads a catalogue entry.
func (r *Repository) ServiceType(ctx context.Context, id string) (domain.ServiceType, error) {
	return serviceType(ctx, r.pool, id)
}

func serviceType(ctx context.Context, q querier, id string) (domain.ServiceType, error) {
	if !isUUID(id) {
		return domain.ServiceType{}, domain.NotFound("service type", id)
	}
	var st domain.ServiceType
	var minutes int32
	var bayType string
	err := q.QueryRow(ctx, `SELECT id, name, duration_minutes, required_skill_id, required_bay_type FROM service_type WHERE id = $1`, id).
		Scan(&st.ID, &st.Name, &minutes, &st.RequiredSkillID, &bayType)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceType{}, domain.NotFound("service type", id)
	}
	if err != nil {
		return domain.ServiceType{}, fmt.Errorf("select service type: %w", err)
	}
	st.Duration = time.Duration(minutes) * time.Minute
	st.RequiredBayType = domain.BayType(bayType)
	return st, nil
}

// VehicleWithOwner loads a vehicle registered with the dealership and its owner.
func (r *Repository) VehicleWithOwner(ctx context.Context, dealershipID, vehicleID string) (domain.Vehicle, domain.Customer, error) {
	return vehicleWithOwner(ctx, r.pool, dealershipID, vehicleID)
}

func vehicleWithOwner(ctx context.Context, q querier, dealershipID, vehicleID string) (domain.Vehicle, domain.Customer, error) {
	if !isUUID(dealershipID) || !isUUID(vehicleID) {
		return domain.Vehicle{}, domain.Customer{}, domain.NotFound("vehicle", vehicleID)
	}
	var v domain.Vehicle
	var c domain.Customer
	err := q.QueryRow(ctx, `
		SELECT v.id, v.dealership_id, v.customer_id, v.vin, v.model, c.id, c.name
		FROM vehicle v JOIN customer c ON c.id = v.customer_id
		WHERE v.id = $1 AND v.dealership_id = $2`, vehicleID, dealershipID).
		Scan(&v.ID, &v.DealershipID, &v.CustomerID, &v.VIN, &v.Model, &c.ID, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Vehicle{}, domain.Customer{}, domain.NotFound("vehicle", vehicleID)
	}
	if err != nil {
		return domain.Vehicle{}, domain.Customer{}, fmt.Errorf("select vehicle: %w", err)
	}
	return v, c, nil
}

// Appointment loads the full FR-3 record.
func (r *Repository) Appointment(ctx context.Context, id string) (domain.Appointment, error) {
	return appointment(ctx, r.pool, id)
}

func appointment(ctx context.Context, q querier, id string) (domain.Appointment, error) {
	if !isUUID(id) {
		return domain.Appointment{}, domain.NotFound("appointment", id)
	}
	var (
		a        domain.Appointment
		status   string
		tz       string
		minutes  int32
		bayType  string
		svcBay   string
		createdT time.Time
	)
	err := q.QueryRow(ctx, `
		SELECT a.id, a.dealership_id, a.status, a.start_time, a.end_time, a.created_at, d.timezone,
		       c.id, c.name,
		       v.id, v.dealership_id, v.customer_id, v.vin, v.model,
		       st.id, st.name, st.duration_minutes, st.required_skill_id, st.required_bay_type,
		       t.id, t.name,
		       b.id, b.name, b.bay_type
		FROM appointment a
		JOIN dealership   d  ON d.id  = a.dealership_id
		JOIN customer     c  ON c.id  = a.customer_id
		JOIN vehicle      v  ON v.id  = a.vehicle_id
		JOIN service_type st ON st.id = a.service_type_id
		JOIN technician   t  ON t.id  = a.technician_id
		JOIN service_bay  b  ON b.id  = a.bay_id
		WHERE a.id = $1`, id).Scan(
		&a.ID, &a.DealershipID, &status, &a.Interval.Start, &a.Interval.End, &createdT, &tz,
		&a.Customer.ID, &a.Customer.Name,
		&a.Vehicle.ID, &a.Vehicle.DealershipID, &a.Vehicle.CustomerID, &a.Vehicle.VIN, &a.Vehicle.Model,
		&a.ServiceType.ID, &a.ServiceType.Name, &minutes, &a.ServiceType.RequiredSkillID, &svcBay,
		&a.Technician.ID, &a.Technician.Name,
		&a.Bay.ID, &a.Bay.Name, &bayType,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Appointment{}, domain.NotFound("appointment", id)
	}
	if err != nil {
		return domain.Appointment{}, fmt.Errorf("select appointment: %w", err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return domain.Appointment{}, fmt.Errorf("appointment %s: invalid dealership timezone %q: %w", id, tz, err)
	}
	a.Status = domain.AppointmentStatus(status)
	a.Interval.Start, a.Interval.End = a.Interval.Start.In(loc), a.Interval.End.In(loc)
	a.CreatedAt = createdT.In(loc)
	a.ServiceType.Duration = time.Duration(minutes) * time.Minute
	a.ServiceType.RequiredBayType = domain.BayType(svcBay)
	a.Bay.Type = domain.BayType(bayType)
	return a, nil
}

// DaySchedule loads every technician (with skills) and bay of the dealership
// plus the CONFIRMED intervals starting within the day. No qualification or
// overlap logic here — the domain decides.
func (r *Repository) DaySchedule(ctx context.Context, q service.ScheduleQuery) (domain.DaySchedule, error) {
	return daySchedule(ctx, r.pool, q)
}

func daySchedule(ctx context.Context, q querier, sq service.ScheduleQuery) (domain.DaySchedule, error) {
	var s domain.DaySchedule
	if !isUUID(sq.DealershipID) {
		return s, domain.NotFound("dealership", sq.DealershipID)
	}

	techRows, err := q.Query(ctx, `
		SELECT t.id, t.name,
		       COALESCE(array_agg(ts.skill_id::text ORDER BY ts.skill_id) FILTER (WHERE ts.skill_id IS NOT NULL), '{}')
		FROM technician t
		LEFT JOIN technician_skill ts ON ts.technician_id = t.id
		WHERE t.dealership_id = $1
		GROUP BY t.id, t.name
		ORDER BY t.id`, sq.DealershipID)
	if err != nil {
		return s, fmt.Errorf("select technicians: %w", err)
	}
	techIdx := map[string]int{}
	for techRows.Next() {
		var t domain.Technician
		if err := techRows.Scan(&t.ID, &t.Name, &t.SkillIDs); err != nil {
			techRows.Close()
			return s, fmt.Errorf("scan technician: %w", err)
		}
		techIdx[t.ID] = len(s.Technicians)
		s.Technicians = append(s.Technicians, domain.TechnicianSchedule{Technician: t})
	}
	techRows.Close()
	if err := techRows.Err(); err != nil {
		return s, err
	}

	bayRows, err := q.Query(ctx, `SELECT id, name, bay_type FROM service_bay WHERE dealership_id = $1 ORDER BY id`, sq.DealershipID)
	if err != nil {
		return s, fmt.Errorf("select bays: %w", err)
	}
	bayIdx := map[string]int{}
	for bayRows.Next() {
		var b domain.Bay
		var bt string
		if err := bayRows.Scan(&b.ID, &b.Name, &bt); err != nil {
			bayRows.Close()
			return s, fmt.Errorf("scan bay: %w", err)
		}
		b.Type = domain.BayType(bt)
		bayIdx[b.ID] = len(s.Bays)
		s.Bays = append(s.Bays, domain.BaySchedule{Bay: b})
	}
	bayRows.Close()
	if err := bayRows.Err(); err != nil {
		return s, err
	}

	apptRows, err := q.Query(ctx, `
		SELECT technician_id, bay_id, vehicle_id, start_time, end_time
		FROM appointment
		WHERE dealership_id = $1 AND status = 'CONFIRMED' AND start_time >= $2 AND start_time < $3`,
		sq.DealershipID, sq.Day.Start, sq.Day.End)
	if err != nil {
		return s, fmt.Errorf("select day appointments: %w", err)
	}
	defer apptRows.Close()
	s.VehicleBooked = []domain.Interval{}
	for apptRows.Next() {
		var techID, bayID, vehicleID string
		var iv domain.Interval
		if err := apptRows.Scan(&techID, &bayID, &vehicleID, &iv.Start, &iv.End); err != nil {
			return s, fmt.Errorf("scan appointment: %w", err)
		}
		if i, ok := techIdx[techID]; ok {
			s.Technicians[i].Booked = append(s.Technicians[i].Booked, iv)
		}
		if i, ok := bayIdx[bayID]; ok {
			s.Bays[i].Booked = append(s.Bays[i].Booked, iv)
		}
		if sq.VehicleID != "" && vehicleID == sq.VehicleID {
			s.VehicleBooked = append(s.VehicleBooked, iv)
		}
	}
	return s, apptRows.Err()
}

// InTx runs fn in a READ COMMITTED transaction.
func (r *Repository) InTx(ctx context.Context, fn func(tx service.BookingTx) error) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		return fn(&bookingTx{tx: tx})
	})
}
