package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// The contention path — every attempt loses a race while resources stay free —
// cannot be provoked reliably against a real database, so these tests drive the
// scheduler through an in-memory repository whose InsertAppointment always
// loses. They run under -short.

const (
	fakeDealershipID = "10000000-0000-4000-8000-0000000000f1"
	fakeVehicleID    = "70000000-0000-4000-8000-0000000000f1"
	fakeCustomerID   = "60000000-0000-4000-8000-0000000000f1"
	fakeServiceType  = "30000000-0000-4000-8000-0000000000f1"
	fakeSkillID      = "20000000-0000-4000-8000-0000000000f1"
)

type fakeRepo struct {
	schedule    domain.DaySchedule
	insertErr   error
	insertCalls int
	saved       []service.IdempotencyRecord
	stored      map[string]service.IdempotencyRecord
}

func newFakeRepo(insertErr error, bays ...domain.BaySchedule) *fakeRepo {
	if bays == nil {
		bays = []domain.BaySchedule{
			{Bay: domain.Bay{ID: "bay-01", Name: "Bay 1", Type: domain.BayTypeGeneral}},
			{Bay: domain.Bay{ID: "bay-02", Name: "Bay 2", Type: domain.BayTypeGeneral}},
		}
	}
	return &fakeRepo{
		insertErr: insertErr,
		stored:    map[string]service.IdempotencyRecord{},
		schedule: domain.DaySchedule{
			Technicians: []domain.TechnicianSchedule{
				{Technician: domain.Technician{ID: "tech-01", Name: "An", SkillIDs: []string{fakeSkillID}}},
				{Technician: domain.Technician{ID: "tech-02", Name: "Binh", SkillIDs: []string{fakeSkillID}}},
			},
			Bays: bays,
		},
	}
}

func (f *fakeRepo) Dealership(context.Context, string) (domain.Dealership, error) {
	return domain.Dealership{
		ID: fakeDealershipID, Name: "Fake Motors", Location: hcm,
		Hours: domain.BusinessHours{
			time.Monday: {OpensAt: 8 * 60, ClosesAt: 17 * 60},
		},
	}, nil
}

func (f *fakeRepo) ServiceType(context.Context, string) (domain.ServiceType, error) {
	return domain.ServiceType{
		ID: fakeServiceType, Name: "Oil change", Duration: 60 * time.Minute,
		RequiredSkillID: fakeSkillID, RequiredBayType: domain.BayTypeGeneral,
	}, nil
}

func (f *fakeRepo) VehicleWithOwner(context.Context, string, string) (domain.Vehicle, domain.Customer, error) {
	return domain.Vehicle{ID: fakeVehicleID, DealershipID: fakeDealershipID, CustomerID: fakeCustomerID, VIN: "FAKEVIN0000000001", Model: "Test"},
		domain.Customer{ID: fakeCustomerID, Name: "Test Owner"}, nil
}

func (f *fakeRepo) Appointment(context.Context, string) (domain.Appointment, error) {
	return domain.Appointment{ID: "appt-01", Status: domain.StatusConfirmed}, nil
}

func (f *fakeRepo) DaySchedule(context.Context, service.ScheduleQuery) (domain.DaySchedule, error) {
	return f.schedule, nil
}

func (f *fakeRepo) InTx(ctx context.Context, fn func(tx service.BookingTx) error) error {
	return fn(f)
}

func (f *fakeRepo) InsertAppointment(context.Context, service.NewAppointment) (domain.Appointment, error) {
	f.insertCalls++
	return domain.Appointment{}, f.insertErr
}

func (f *fakeRepo) LockIdempotencyKey(context.Context, string, string) error     { return nil }
func (f *fakeRepo) LockSchedulingDay(context.Context, string, domain.Date) error { return nil }

func (f *fakeRepo) FindIdempotencyRecord(_ context.Context, _, key string, _ time.Time) (*service.IdempotencyRecord, error) {
	if rec, ok := f.stored[key]; ok {
		return &rec, nil
	}
	return nil, nil
}

func (f *fakeRepo) SaveIdempotencyRecord(_ context.Context, rec service.IdempotencyRecord) error {
	f.saved = append(f.saved, rec)
	f.stored[rec.Key] = rec
	return nil
}

// lostRace builds what mapInsertError produces for a database rejection.
func lostRace(code domain.Code, msg string, conflicting ...domain.ResourceKind) error {
	return fmtLostRace(&domain.Error{Code: code, Message: msg, Conflicting: conflicting})
}

func bookFake(t *testing.T, repo *fakeRepo, key string) (domain.Appointment, error) {
	t.Helper()
	svc := service.New(repo, service.WithClock(func() time.Time { return local(monday, 7, 0) }))
	return svc.Book(context.Background(), service.BookRequest{
		DealershipID:   fakeDealershipID,
		VehicleID:      fakeVehicleID,
		ServiceTypeID:  fakeServiceType,
		StartTime:      local(monday, 9, 0),
		IdempotencyKey: key,
	})
}

func TestBooking_ContentionIsReportedAsRetryableNotAsUnavailability(t *testing.T) {
	// Every insert loses a race, but selection on fresh data keeps succeeding:
	// the resources are free, the request simply never won. That is contention.
	repo := newFakeRepo(lostRace(domain.CodeContention, "concurrent booking contention"))
	_, err := bookFake(t, repo, "")
	de := expectCode(t, err, domain.CodeContention)
	if de.Conflicting != nil {
		t.Fatalf("contention names no scarce resource, got conflicting = %v", de.Conflicting)
	}
}

func TestBooking_ContentionMessageLeaksNoDatabaseInternals(t *testing.T) {
	repo := newFakeRepo(lostRace(domain.CodeContention, "concurrent booking contention"))
	_, err := bookFake(t, repo, "")
	de := expectCode(t, err, domain.CodeContention)
	for _, banned := range []string{"deadlock", "40P01", "23P01", "SQLSTATE", "constraint", "appointment_no_"} {
		if strings.Contains(strings.ToLower(de.Message), strings.ToLower(banned)) {
			t.Errorf("client-facing message must not contain %q: %q", banned, de.Message)
		}
	}
}

func TestBooking_ContentionIsNotRecordedUnderAnIdempotencyKey(t *testing.T) {
	repo := newFakeRepo(lostRace(domain.CodeContention, "concurrent booking contention"))
	key := "8d2c8a52-1b1e-4c65-9b26-0000000000c1"
	_, err := bookFake(t, repo, key)
	_ = expectCode(t, err, domain.CodeContention)
	if len(repo.saved) != 0 {
		t.Fatalf("a transient outcome must not be remembered, got %+v", repo.saved)
	}
	// A retry with the same key must re-evaluate rather than replay.
	before := repo.insertCalls
	_, err = bookFake(t, repo, key)
	_ = expectCode(t, err, domain.CodeContention)
	if repo.insertCalls <= before {
		t.Fatal("the retry must re-run selection, not replay a stored rejection")
	}
}

func TestBooking_BudgetIsQualifiedResourcesPlusOneWithAFloorOfThree(t *testing.T) {
	// Two qualified technicians and two qualified bays -> max(3, 2+1) = 3.
	repo := newFakeRepo(lostRace(domain.CodeContention, "concurrent booking contention"))
	if _, err := bookFake(t, repo, ""); err == nil {
		t.Fatal("expected contention")
	}
	if repo.insertCalls != 3 {
		t.Fatalf("insert attempts = %d, want 3 (max(3, min(2 techs, 2 bays)+1))", repo.insertCalls)
	}

	// Four qualified bays and two technicians -> min is 2, so still 3.
	bays := []domain.BaySchedule{
		{Bay: domain.Bay{ID: "bay-01", Type: domain.BayTypeGeneral}},
		{Bay: domain.Bay{ID: "bay-02", Type: domain.BayTypeGeneral}},
		{Bay: domain.Bay{ID: "bay-03", Type: domain.BayTypeGeneral}},
		{Bay: domain.Bay{ID: "bay-04", Type: domain.BayTypeGeneral}},
	}
	repo = newFakeRepo(lostRace(domain.CodeContention, "concurrent booking contention"), bays...)
	if _, err := bookFake(t, repo, ""); err == nil {
		t.Fatal("expected contention")
	}
	if repo.insertCalls != 3 {
		t.Fatalf("insert attempts = %d, want 3; the budget must not grow per lost race", repo.insertCalls)
	}
}

func TestBooking_GenuineExhaustionIsStillNoAvailableResourceAndIsRecorded(t *testing.T) {
	// No qualifying bay at all: selection fails on fresh data, so the outcome is
	// durable and must keep its precise code and be remembered under the key.
	repo := newFakeRepo(nil, domain.BaySchedule{Bay: domain.Bay{ID: "bay-ev", Type: domain.BayTypeEV}})
	key := "8d2c8a52-1b1e-4c65-9b26-0000000000c2"
	_, err := bookFake(t, repo, key)
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	if len(de.Conflicting) != 1 || de.Conflicting[0] != domain.ResourceBay {
		t.Fatalf("conflicting = %v, want [BAY]", de.Conflicting)
	}
	if len(repo.saved) != 1 || repo.saved[0].ErrorCode != domain.CodeNoAvailableResource {
		t.Fatalf("a durable rejection must be recorded, got %+v", repo.saved)
	}
}
