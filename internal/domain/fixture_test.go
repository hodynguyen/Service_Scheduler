package domain

import (
	"testing"
	"time"
)

// In-memory mirror of the seeded dealership (see
// internal/repository/postgres/seed). Identifiers sort the same way as the
// seed so BR-6 tie-break expectations transfer between test layers.

var hcm = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		panic(err)
	}
	return loc
}()

// Mon–Fri 08:00–17:00, Sat 08:00–12:00, Sunday closed.
var weekHours = BusinessHours{
	time.Monday:    {OpensAt: 8 * 60, ClosesAt: 17 * 60},
	time.Tuesday:   {OpensAt: 8 * 60, ClosesAt: 17 * 60},
	time.Wednesday: {OpensAt: 8 * 60, ClosesAt: 17 * 60},
	time.Thursday:  {OpensAt: 8 * 60, ClosesAt: 17 * 60},
	time.Friday:    {OpensAt: 8 * 60, ClosesAt: 17 * 60},
	time.Saturday:  {OpensAt: 8 * 60, ClosesAt: 12 * 60},
}

// 2030-03-04 is a Monday, 03-09 a Saturday, 03-10 a Sunday.
const (
	monday   = 4
	saturday = 9
	sunday   = 10
)

func local(day, hh, mm int) time.Time {
	return time.Date(2030, time.March, day, hh, mm, 0, 0, hcm)
}

// early is a "now" before opening on the Monday so every slot is in the future.
var early = local(monday, 7, 0)

const (
	skillGeneral      = "skill-01-general"
	skillAlignment    = "skill-02-alignment"
	skillEV           = "skill-03-ev"
	skillTransmission = "skill-04-transmission"
)

var (
	techAn   = Technician{ID: "tech-01", Name: "An", SkillIDs: []string{skillGeneral, skillAlignment}}
	techBinh = Technician{ID: "tech-02", Name: "Binh", SkillIDs: []string{skillGeneral, skillTransmission}}
	techChi  = Technician{ID: "tech-03", Name: "Chi", SkillIDs: []string{skillGeneral, skillEV}}
	techDung = Technician{ID: "tech-04", Name: "Dung", SkillIDs: []string{skillGeneral}}

	bay1         = Bay{ID: "bay-01", Name: "Bay 1", Type: BayTypeGeneral}
	bay2         = Bay{ID: "bay-02", Name: "Bay 2", Type: BayTypeGeneral}
	bayAlignment = Bay{ID: "bay-03", Name: "Alignment rig", Type: BayTypeAlignment}
	bayEV        = Bay{ID: "bay-04", Name: "EV bay", Type: BayTypeEV}

	svcOil          = ServiceType{ID: "svc-01", Name: "Oil change", Duration: 30 * time.Minute, RequiredSkillID: skillGeneral, RequiredBayType: BayTypeGeneral}
	svcAlignment    = ServiceType{ID: "svc-02", Name: "Wheel alignment", Duration: 60 * time.Minute, RequiredSkillID: skillAlignment, RequiredBayType: BayTypeAlignment}
	svcEV           = ServiceType{ID: "svc-03", Name: "EV diagnostic", Duration: 90 * time.Minute, RequiredSkillID: skillEV, RequiredBayType: BayTypeEV}
	svcTransmission = ServiceType{ID: "svc-04", Name: "Transmission", Duration: 180 * time.Minute, RequiredSkillID: skillTransmission, RequiredBayType: BayTypeGeneral}

	allTechs = []Technician{techAn, techBinh, techChi, techDung}
	allBays  = []Bay{bay1, bay2, bayAlignment, bayEV}
)

// schedule builds a DaySchedule for the whole dealership with the given
// confirmed intervals per technician / bay id.
func schedule(techBooked, bayBooked map[string][]Interval) DaySchedule {
	var s DaySchedule
	for _, t := range allTechs {
		s.Technicians = append(s.Technicians, TechnicianSchedule{Technician: t, Booked: techBooked[t.ID]})
	}
	for _, b := range allBays {
		s.Bays = append(s.Bays, BaySchedule{Bay: b, Booked: bayBooked[b.ID]})
	}
	return s
}

func iv(day, hh, mm int, d time.Duration) Interval {
	return NewInterval(local(day, hh, mm), d)
}

func expectCode(t *testing.T, err error, code Code) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil error", code)
	}
	de, ok := AsError(err)
	if !ok {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if de.Code != code {
		t.Fatalf("code = %s, want %s (%v)", de.Code, code, err)
	}
	return de
}

func expectConflicting(t *testing.T, de *Error, want ...ResourceKind) {
	t.Helper()
	if len(de.Conflicting) != len(want) {
		t.Fatalf("conflicting = %v, want %v", de.Conflicting, want)
	}
	for i := range want {
		if de.Conflicting[i] != want[i] {
			t.Fatalf("conflicting = %v, want %v", de.Conflicting, want)
		}
	}
}
