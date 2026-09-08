package domain

import "time"

// BayType is the capability class of a service bay (glossary §4).
type BayType string

const (
	BayTypeGeneral   BayType = "GENERAL"
	BayTypeAlignment BayType = "ALIGNMENT"
	BayTypeEV        BayType = "EV"
)

// ParseBayType validates a bay type string.
func ParseBayType(s string) (BayType, bool) {
	switch BayType(s) {
	case BayTypeGeneral, BayTypeAlignment, BayTypeEV:
		return BayType(s), true
	}
	return "", false
}

// AppointmentStatus distinguishes active reservations from released ones.
type AppointmentStatus string

const (
	StatusConfirmed AppointmentStatus = "CONFIRMED"
	StatusCancelled AppointmentStatus = "CANCELLED"
)

// Dealership owns bays, employs technicians and defines the local clock.
type Dealership struct {
	ID       string
	Name     string
	Location *time.Location // IANA timezone (A-9)
	Hours    BusinessHours
}

// Skill is a capability or certification a technician may hold.
type Skill struct {
	ID   string
	Code string
	Name string
}

// ServiceType is a catalogue entry: duration, required skill, required bay type (A-1..A-3).
type ServiceType struct {
	ID              string
	Name            string
	Duration        time.Duration
	RequiredSkillID string
	RequiredBayType BayType
}

// Technician is a scheduled resource holding zero or more skills.
type Technician struct {
	ID       string
	Name     string
	SkillIDs []string
}

// HasSkill reports whether the technician holds the given skill.
func (t Technician) HasSkill(skillID string) bool {
	for _, s := range t.SkillIDs {
		if s == skillID {
			return true
		}
	}
	return false
}

// Bay is a typed physical working position.
type Bay struct {
	ID   string
	Name string
	Type BayType
}

// Customer is the vehicle owner; derived from the vehicle, never supplied (A-6).
type Customer struct {
	ID   string
	Name string
}

// Vehicle is registered with one dealership and owned by one customer.
type Vehicle struct {
	ID           string
	DealershipID string
	CustomerID   string
	VIN          string
	Model        string
}

// Appointment is a confirmed reservation binding customer, vehicle,
// technician, bay and time range (FR-3 shape).
type Appointment struct {
	ID           string
	DealershipID string
	Status       AppointmentStatus
	Interval     Interval
	Customer     Customer
	Vehicle      Vehicle
	ServiceType  ServiceType
	Technician   Technician
	Bay          Bay
	CreatedAt    time.Time
}
