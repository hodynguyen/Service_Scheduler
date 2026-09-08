package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/repository/postgres"
	"github.com/hodynguyen/service-scheduler/internal/service"
	"github.com/hodynguyen/service-scheduler/internal/testutil/pgtest"
)

// These tests run the real service against a real Postgres. They are the
// executable form of AC-01..AC-24 below the HTTP layer.

var hcm = func() *time.Location {
	l, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		panic(err)
	}
	return l
}()

const (
	monday   = 4 // 2030-03-04
	saturday = 9
	sunday   = 10
)

func local(day, hh, mm int) time.Time { return time.Date(2030, time.March, day, hh, mm, 0, 0, hcm) }

// clock is a mutable fixed "now" — Monday 07:00 local unless a test moves it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }

type harness struct {
	svc   *service.Scheduler
	pool  *pgxpool.Pool
	clock *clock
	ctx   context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.Reset(t, pool)
	c := &clock{now: local(monday, 7, 0)}
	svc := service.New(postgres.New(pool), service.WithClock(c.Now))
	return &harness{svc: svc, pool: pool, clock: c, ctx: context.Background()}
}

func (h *harness) book(vehicle, serviceType string, start time.Time) (domain.Appointment, error) {
	return h.svc.Book(h.ctx, service.BookRequest{
		DealershipID:  postgres.SeedDealershipID,
		VehicleID:     vehicle,
		ServiceTypeID: serviceType,
		StartTime:     start,
	})
}

func (h *harness) mustBook(t *testing.T, vehicle, serviceType string, start time.Time) domain.Appointment {
	t.Helper()
	a, err := h.book(vehicle, serviceType, start)
	if err != nil {
		t.Fatalf("booking %s at %s must succeed: %v", serviceType, start.Format("15:04"), err)
	}
	return a
}

func (h *harness) confirmedCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM appointment WHERE status = 'CONFIRMED'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func expectCode(t *testing.T, err error, code domain.Code) *domain.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got success", code)
	}
	de, ok := domain.AsError(err)
	if !ok {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if de.Code != code {
		t.Fatalf("code = %s, want %s (%v)", de.Code, code, err)
	}
	return de
}

func expectConflicting(t *testing.T, de *domain.Error, want ...domain.ResourceKind) {
	t.Helper()
	if fmt.Sprint(de.Conflicting) != fmt.Sprint(want) {
		t.Fatalf("conflicting = %v, want %v", de.Conflicting, want)
	}
}

// --- Happy path -----------------------------------------------------------

func TestBooking_AC01_AvailableSlotReturnsAssignedTechnicianAndBay(t *testing.T) {
	h := newHarness(t)
	a := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 9, 0))
	if a.ID == "" || a.Status != domain.StatusConfirmed {
		t.Fatalf("appointment = %+v", a)
	}
	if a.Technician.ID != postgres.SeedTechnicianAnID || a.Bay.ID != postgres.SeedBayAlignmentID {
		t.Fatalf("assigned %s/%s, want An on the alignment rig", a.Technician.Name, a.Bay.Name)
	}
	if a.Technician.Name == "" || a.Bay.Name == "" || a.Bay.Type != domain.BayTypeAlignment {
		t.Fatalf("assigned resources must be fully populated: %+v", a)
	}
}

func TestBooking_AC02_EndTimeIsStartPlusServiceDuration(t *testing.T) {
	h := newHarness(t)
	a := h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	if !a.Interval.End.Equal(local(monday, 10, 30)) {
		t.Fatalf("end = %s, want 10:30 (90 min)", a.Interval.End.In(hcm))
	}
	if a.ServiceType.Duration != 90*time.Minute {
		t.Fatalf("service type duration = %v", a.ServiceType.Duration)
	}
}

func TestBooking_AC03_CustomerIsTheVehicleOwner(t *testing.T) {
	h := newHarness(t)
	a := h.mustBook(t, postgres.SeedVehicleRangerID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	if a.Customer.ID != postgres.SeedCustomerLinhID || a.Customer.Name != "Linh Hoang" {
		t.Fatalf("customer = %+v, want Linh Hoang (owner of the Ranger)", a.Customer)
	}
	if a.Vehicle.ID != postgres.SeedVehicleRangerID || a.Vehicle.VIN == "" || a.Vehicle.Model == "" {
		t.Fatalf("vehicle = %+v", a.Vehicle)
	}
}

func TestBooking_FR3_GetAppointmentReturnsFullRecord(t *testing.T) {
	h := newHarness(t)
	created := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 9, 0))
	got, err := h.svc.GetAppointment(h.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Technician.ID != created.Technician.ID || got.Technician.Name != created.Technician.Name ||
		got.Bay != created.Bay || got.Customer != created.Customer || got.Vehicle != created.Vehicle ||
		got.ServiceType != created.ServiceType || got.Status != domain.StatusConfirmed ||
		!got.Interval.Start.Equal(created.Interval.Start) || !got.Interval.End.Equal(created.Interval.End) {
		t.Fatalf("GetAppointment = %+v\nwant %+v", got, created)
	}
	_, err = h.svc.GetAppointment(h.ctx, "00000000-0000-4000-8000-00000000dead")
	expectCode(t, err, domain.CodeResourceNotFound)
}

func TestBooking_UnknownReferencesAreNotFound(t *testing.T) {
	h := newHarness(t)
	unknown := "00000000-0000-4000-8000-00000000dead"
	_, err := h.svc.Book(h.ctx, service.BookRequest{DealershipID: unknown, VehicleID: postgres.SeedVehicleCamryID, ServiceTypeID: postgres.SeedServiceTypeOilChangeID, StartTime: local(monday, 9, 0)})
	expectCode(t, err, domain.CodeResourceNotFound)
	_, err = h.book(unknown, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	expectCode(t, err, domain.CodeResourceNotFound)
	_, err = h.book(postgres.SeedVehicleCamryID, unknown, local(monday, 9, 0))
	expectCode(t, err, domain.CodeResourceNotFound)
}

func TestBooking_BR7_VehicleFromAnotherDealershipIsNotFound(t *testing.T) {
	h := newHarness(t)
	var otherDealership, otherVehicle string
	if err := h.pool.QueryRow(h.ctx, `INSERT INTO dealership (name, timezone) VALUES ('Other', 'Europe/London') RETURNING id`).Scan(&otherDealership); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(h.ctx, `INSERT INTO vehicle (dealership_id, customer_id, vin, model) VALUES ($1, $2, 'OTHERVIN000000001', 'Mini') RETURNING id`, otherDealership, postgres.SeedCustomerMaiID).Scan(&otherVehicle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(h.ctx, `DELETE FROM vehicle WHERE id = $1`, otherVehicle)
		_, _ = h.pool.Exec(h.ctx, `DELETE FROM dealership WHERE id = $1`, otherDealership)
	})
	_, err := h.book(otherVehicle, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	expectCode(t, err, domain.CodeResourceNotFound)
}

// --- Resource conflicts ---------------------------------------------------

func TestBooking_AC04_AllQualifyingBaysOccupiedReportsBay(t *testing.T) {
	h := newHarness(t)
	// Two GENERAL bays; four GENERAL_SERVICE technicians.
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	_, err := h.book(postgres.SeedVehicleCRVID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	expectConflicting(t, de, domain.ResourceBay)
}

func TestBooking_AC05_AllQualifyingTechniciansOccupiedReportsTechnician(t *testing.T) {
	h := newHarness(t)
	// Transmission: only Binh qualifies; the second GENERAL bay stays free.
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeTransmissionID, local(monday, 9, 0))
	_, err := h.book(postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeTransmissionID, local(monday, 9, 0))
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	expectConflicting(t, de, domain.ResourceTechnician)
}

func TestBooking_AC06_BothOccupiedReportsBoth(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	expectConflicting(t, de, domain.ResourceBay, domain.ResourceTechnician)
}

func TestBooking_AC07_VehicleWithOverlappingAppointmentIsRejected(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	// Plenty of free resources for a second oil change; the vehicle is the problem.
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 15))
	expectCode(t, err, domain.CodeVehicleAlreadyBooked)
	if h.confirmedCount(t) != 1 {
		t.Fatal("rejected booking must not write anything")
	}
}

// --- Qualification --------------------------------------------------------

func TestBooking_AC08_FreeButUnqualifiedTechnicianIsNeverAssigned(t *testing.T) {
	h := newHarness(t)
	// Binh (the only TRANSMISSION holder) is busy; Dung is free but unqualified.
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeTransmissionID, local(monday, 9, 0))
	_, err := h.book(postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeTransmissionID, local(monday, 10, 0))
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	expectConflicting(t, de, domain.ResourceTechnician)
	var n int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM appointment WHERE technician_id = $1`, postgres.SeedTechnicianDungID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("Dung must never be assigned transmission work")
	}
}

func TestBooking_AC09_FreeBayOfWrongTypeIsNeverAssigned(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	// Bays 1, 2 and the alignment rig are free but none is an EV bay.
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 30))
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	if len(de.Conflicting) == 0 || de.Conflicting[0] != domain.ResourceBay {
		t.Fatalf("conflicting must include BAY first, got %v", de.Conflicting)
	}
}

// --- Time boundaries ------------------------------------------------------

func TestBooking_AC10_AC11_AdjacentAppointmentsShareResources(t *testing.T) {
	h := newHarness(t)
	first := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 9, 0)) // [09:00, 10:00)
	after := h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeAlignmentID, local(monday, 10, 0))  // starts when first ends
	before := h.mustBook(t, postgres.SeedVehicleCRVID, postgres.SeedServiceTypeAlignmentID, local(monday, 8, 0))  // ends when first starts
	for _, a := range []domain.Appointment{after, before} {
		if a.Technician.ID != first.Technician.ID || a.Bay.ID != first.Bay.ID {
			t.Fatalf("adjacent bookings must reuse the only qualifying resources: %+v", a)
		}
	}
}

func TestBooking_AC12_OneMinuteOverlapIsRejected(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 9, 0)) // [09:00, 10:00)
	_, err := h.book(postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeAlignmentID, local(monday, 9, 59))
	de := expectCode(t, err, domain.CodeNoAvailableResource)
	expectConflicting(t, de, domain.ResourceBay, domain.ResourceTechnician)
	_, err = h.book(postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeAlignmentID, local(monday, 8, 1))
	expectCode(t, err, domain.CodeNoAvailableResource)
}

func TestBooking_AC13_StartBeforeOpeningIsOutsideBusinessHours(t *testing.T) {
	h := newHarness(t)
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 7, 30))
	expectCode(t, err, domain.CodeOutsideBusinessHours)
}

func TestBooking_AC14_EndingAfterClosingIsServiceExceedsClosingTime(t *testing.T) {
	h := newHarness(t)
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 16, 30))
	expectCode(t, err, domain.CodeServiceExceedsClosingTime)
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 16, 0)) // ends exactly at 17:00
}

func TestBooking_AC15_ClosedDayIsRejected(t *testing.T) {
	h := newHarness(t)
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(sunday, 9, 0))
	expectCode(t, err, domain.CodeOutsideBusinessHours)
}

func TestBooking_AC16_StartInPastIsRejected(t *testing.T) {
	h := newHarness(t)
	h.clock.Set(local(monday, 10, 0))
	_, err := h.book(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	expectCode(t, err, domain.CodeStartTimeInPast)
}

// --- Cancellation semantics -----------------------------------------------

func TestBooking_AC17_CancelledAppointmentReleasesResources(t *testing.T) {
	h := newHarness(t)
	first := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	if _, err := h.pool.Exec(h.ctx, `UPDATE appointment SET status = 'CANCELLED' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	// Same vehicle, same resources, same interval: everything was released (BR-9).
	second := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	if second.Technician.ID != first.Technician.ID || second.Bay.ID != first.Bay.ID {
		t.Fatalf("expected the released resources to be reassigned: %+v", second)
	}
}

// --- Concurrency ----------------------------------------------------------

func TestBooking_AC18_ConcurrentRequestsForOneSlotYieldExactlyOneSuccess(t *testing.T) {
	h := newHarness(t)
	const n = 20
	// EV diagnostic: exactly one qualifying bay and one qualifying technician.
	// Each request uses its own vehicle so the only shared resources are the
	// bay and the technician (see the identical-request variant below).
	vehicles := make([]string, n)
	for i := range vehicles {
		if err := h.pool.QueryRow(h.ctx,
			`INSERT INTO vehicle (dealership_id, customer_id, vin, model) VALUES ($1, $2, $3, 'Test EV') RETURNING id`,
			postgres.SeedDealershipID, postgres.SeedCustomerQuangID, fmt.Sprintf("CONCURRENTVIN%04d", i)).Scan(&vehicles[i]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		pgtest.Reset(t, h.pool)
		_, _ = h.pool.Exec(h.ctx, `DELETE FROM vehicle WHERE vin LIKE 'CONCURRENTVIN%'`)
	})

	var wg sync.WaitGroup
	results := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = h.book(vehicles[i], postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
		}(i)
	}
	close(start)
	wg.Wait()

	var ok, rejected int
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case domain.CodeOf(err) == domain.CodeNoAvailableResource:
			rejected++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || rejected != n-1 {
		t.Fatalf("successes = %d, NO_AVAILABLE_RESOURCE = %d; want 1 and %d", ok, rejected, n-1)
	}
	if h.confirmedCount(t) != 1 {
		t.Fatalf("exactly one CONFIRMED row must exist, got %d", h.confirmedCount(t))
	}
	assertNoInvariantViolated(t, h.pool)
}

func TestBooking_AC18_ConcurrentIdenticalRequestsYieldExactlyOneSuccess(t *testing.T) {
	h := newHarness(t)
	const n = 12
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = h.book(postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
		}(i)
	}
	wg.Wait()
	var ok int
	for _, err := range results {
		if err == nil {
			ok++
			continue
		}
		// Literally identical requests share the vehicle too, so INV-3 is the
		// first invariant the losers hit and VEHICLE_ALREADY_BOOKED is reported.
		if c := domain.CodeOf(err); c != domain.CodeVehicleAlreadyBooked && c != domain.CodeNoAvailableResource {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || h.confirmedCount(t) != 1 {
		t.Fatalf("successes = %d, confirmed rows = %d; want 1 and 1", ok, h.confirmedCount(t))
	}
	assertNoInvariantViolated(t, h.pool)
}

func TestBooking_ConcurrentRequestsSpreadOverSpareResourcesWithoutSpuriousRejection(t *testing.T) {
	h := newHarness(t)
	// Two GENERAL bays and four GENERAL_SERVICE technicians: two concurrent oil
	// changes must BOTH succeed even though both initially choose Bay 1 / An.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, v := range []string{postgres.SeedVehicleCamryID, postgres.SeedVehicleCRVID} {
		wg.Add(1)
		go func(i int, v string) {
			defer wg.Done()
			_, results[i] = h.book(v, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
		}(i, v)
	}
	wg.Wait()
	for _, err := range results {
		if err != nil {
			t.Fatalf("losing the race for Bay 1 must retry onto Bay 2, got %v", err)
		}
	}
	if h.confirmedCount(t) != 2 {
		t.Fatalf("confirmed = %d, want 2", h.confirmedCount(t))
	}
}

// assertNoInvariantViolated checks INV-1..INV-3 by brute force, independent of
// the exclusion constraints.
func assertNoInvariantViolated(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, col := range []string{"bay_id", "technician_id", "vehicle_id"} {
		var n int
		q := fmt.Sprintf(`SELECT count(*) FROM appointment a JOIN appointment b
			ON a.id < b.id AND a.%[1]s = b.%[1]s
			AND a.status = 'CONFIRMED' AND b.status = 'CONFIRMED'
			AND a.start_time < b.end_time AND b.start_time < a.end_time`, col)
		if err := pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("invariant violated: %d overlapping CONFIRMED pairs on %s", n, col)
		}
	}
}

// --- Idempotency ----------------------------------------------------------

func TestBooking_AC19_SameIdempotencyKeyReturnsTheFirstResult(t *testing.T) {
	h := newHarness(t)
	req := service.BookRequest{
		DealershipID:   postgres.SeedDealershipID,
		VehicleID:      postgres.SeedVehicleCamryID,
		ServiceTypeID:  postgres.SeedServiceTypeAlignmentID,
		StartTime:      local(monday, 9, 0),
		IdempotencyKey: "8d2c8a52-1b1e-4c65-9b26-000000000001",
	}
	first, err := h.svc.Book(h.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.Book(h.ctx, req)
	if err != nil {
		t.Fatalf("replay must return the original result, got %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("replay returned %s, want %s", second.ID, first.ID)
	}
	if h.confirmedCount(t) != 1 {
		t.Fatalf("one appointment expected, got %d", h.confirmedCount(t))
	}
}

func TestBooking_AC19_IdempotencyKeyIsScopedPerDealership(t *testing.T) {
	h := newHarness(t)
	// A different key for the same payload creates a second, distinct rejection or success —
	// here the second key sees the vehicle busy, proving the key (not the payload) is what replays.
	base := service.BookRequest{DealershipID: postgres.SeedDealershipID, VehicleID: postgres.SeedVehicleCamryID, ServiceTypeID: postgres.SeedServiceTypeOilChangeID, StartTime: local(monday, 9, 0)}
	a := base
	a.IdempotencyKey = "8d2c8a52-1b1e-4c65-9b26-000000000011"
	if _, err := h.svc.Book(h.ctx, a); err != nil {
		t.Fatal(err)
	}
	b := base
	b.IdempotencyKey = "8d2c8a52-1b1e-4c65-9b26-000000000012"
	_, err := h.svc.Book(h.ctx, b)
	expectCode(t, err, domain.CodeVehicleAlreadyBooked)
}

func TestBooking_AC19_SameKeyDifferentPayloadIsRejected(t *testing.T) {
	h := newHarness(t)
	req := service.BookRequest{DealershipID: postgres.SeedDealershipID, VehicleID: postgres.SeedVehicleCamryID, ServiceTypeID: postgres.SeedServiceTypeOilChangeID, StartTime: local(monday, 9, 0), IdempotencyKey: "8d2c8a52-1b1e-4c65-9b26-000000000021"}
	if _, err := h.svc.Book(h.ctx, req); err != nil {
		t.Fatal(err)
	}
	req.StartTime = local(monday, 10, 0)
	_, err := h.svc.Book(h.ctx, req)
	expectCode(t, err, domain.CodeIdempotencyKeyReused)
	if h.confirmedCount(t) != 1 {
		t.Fatal("a reused key must not create a second appointment")
	}
}

func TestBooking_AC19_RejectionsAreReplayedToo(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, local(monday, 9, 0))
	req := service.BookRequest{DealershipID: postgres.SeedDealershipID, VehicleID: postgres.SeedVehicleCamryID, ServiceTypeID: postgres.SeedServiceTypeEVDiagnosticID, StartTime: local(monday, 9, 0), IdempotencyKey: "8d2c8a52-1b1e-4c65-9b26-000000000031"}
	_, err := h.svc.Book(h.ctx, req)
	first := expectCode(t, err, domain.CodeNoAvailableResource)
	_, err = h.svc.Book(h.ctx, req)
	second := expectCode(t, err, domain.CodeNoAvailableResource)
	expectConflicting(t, second, first.Conflicting...)
}

func TestBooking_AC19_ConcurrentRequestsWithSameKeyCreateOneAppointment(t *testing.T) {
	h := newHarness(t)
	req := service.BookRequest{DealershipID: postgres.SeedDealershipID, VehicleID: postgres.SeedVehicleCamryID, ServiceTypeID: postgres.SeedServiceTypeOilChangeID, StartTime: local(monday, 9, 0), IdempotencyKey: "8d2c8a52-1b1e-4c65-9b26-000000000041"}
	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, err := h.svc.Book(h.ctx, req)
			ids[i], errs[i] = a.ID, err
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("all replays must return the same appointment: %v", ids)
		}
	}
	if h.confirmedCount(t) != 1 {
		t.Fatalf("confirmed = %d, want 1", h.confirmedCount(t))
	}
}

// --- Assignment policy ----------------------------------------------------

func TestBooking_AC20_LessLoadedTechnicianAndBayAreAssigned(t *testing.T) {
	h := newHarness(t)
	first := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	if first.Technician.ID != postgres.SeedTechnicianAnID || first.Bay.ID != postgres.SeedBay1ID {
		t.Fatalf("fresh day: expected An / Bay 1 by id tie-break, got %s / %s", first.Technician.Name, first.Bay.Name)
	}
	// An and Bay 1 now carry 30 minutes; a non-overlapping slot must go to Binh / Bay 2.
	second := h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeOilChangeID, local(monday, 14, 0))
	if second.Technician.ID != postgres.SeedTechnicianBinhID || second.Bay.ID != postgres.SeedBay2ID {
		t.Fatalf("expected Binh / Bay 2 (less loaded), got %s / %s", second.Technician.Name, second.Bay.Name)
	}
}

func TestBooking_AC20_LoadIsCountedPerLocalDate(t *testing.T) {
	h := newHarness(t)
	// Load An heavily on Tuesday; Monday assignment must ignore it.
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday+1, 9, 0))
	got := h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
	if got.Technician.ID != postgres.SeedTechnicianAnID {
		t.Fatalf("Tuesday load must not count for Monday; got %s", got.Technician.Name)
	}
}

func TestBooking_AC21_EqualLoadAssignsAscendingIdentifierRepeatably(t *testing.T) {
	h := newHarness(t)
	for run := 0; run < 5; run++ {
		pgtest.Reset(t, h.pool)
		got := h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, local(monday, 9, 0))
		if got.Technician.ID != postgres.SeedTechnicianAnID || got.Bay.ID != postgres.SeedBay1ID {
			t.Fatalf("run %d: got %s / %s, want An / Bay 1", run, got.Technician.ID, got.Bay.ID)
		}
	}
}

// --- Availability ---------------------------------------------------------

func (h *harness) availability(t *testing.T, serviceType, date, vehicle string) []string {
	t.Helper()
	res, err := h.svc.Availability(h.ctx, service.AvailabilityQuery{DealershipID: postgres.SeedDealershipID, ServiceTypeID: serviceType, Date: date, VehicleID: vehicle})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(res.Slots))
	for i, s := range res.Slots {
		out[i] = s.In(hcm).Format("15:04")
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestAvailability_AC22_ExcludesSlotsThatWouldFailAnInvariant(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, local(monday, 10, 0)) // An + rig 10:00–11:00
	slots := h.availability(t, postgres.SeedServiceTypeAlignmentID, "2030-03-04", "")
	for _, s := range []string{"09:30", "10:00", "10:30"} {
		if contains(slots, s) {
			t.Fatalf("%s overlaps the 10:00 booking; slots = %v", s, slots)
		}
	}
	for _, s := range []string{"08:00", "09:00", "11:00", "16:00"} {
		if !contains(slots, s) {
			t.Fatalf("%s must be available; slots = %v", s, slots)
		}
	}
	// With the vehicle supplied, INV-3 is applied as well.
	withVehicle := h.availability(t, postgres.SeedServiceTypeOilChangeID, "2030-03-04", postgres.SeedVehicleCamryID)
	if contains(withVehicle, "10:00") || contains(withVehicle, "10:30") {
		t.Fatalf("vehicle is busy 10:00–11:00; slots = %v", withVehicle)
	}
}

func TestAvailability_AC23_ExcludesSlotsRunningPastClosing(t *testing.T) {
	h := newHarness(t)
	slots := h.availability(t, postgres.SeedServiceTypeEVDiagnosticID, "2030-03-09", "") // Saturday 08:00–12:00, 90 min
	want := []string{"08:00", "08:30", "09:00", "09:30", "10:00", "10:30"}
	if fmt.Sprint(slots) != fmt.Sprint(want) {
		t.Fatalf("slots = %v, want %v", slots, want)
	}
}

func TestAvailability_AC24_FullyBookedDayReturnsEmptyArray(t *testing.T) {
	h := newHarness(t)
	h.mustBook(t, postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, local(saturday, 8, 0))    // 08:00–09:30
	h.mustBook(t, postgres.SeedVehicleCamryID, postgres.SeedServiceTypeEVDiagnosticID, local(saturday, 9, 30)) // 09:30–11:00
	res, err := h.svc.Availability(h.ctx, service.AvailabilityQuery{DealershipID: postgres.SeedDealershipID, ServiceTypeID: postgres.SeedServiceTypeEVDiagnosticID, Date: "2030-03-09"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Slots == nil || len(res.Slots) != 0 {
		t.Fatalf("want empty non-nil slots, got %v", res.Slots)
	}
	if res.ServiceType.ID != postgres.SeedServiceTypeEVDiagnosticID || res.ServiceType.Duration != 90*time.Minute {
		t.Fatalf("service type echo = %+v", res.ServiceType)
	}
}

func TestAvailability_ClosedDayReturnsEmptyArrayNotError(t *testing.T) {
	h := newHarness(t)
	slots := h.availability(t, postgres.SeedServiceTypeOilChangeID, "2030-03-10", "")
	if len(slots) != 0 {
		t.Fatalf("Sunday is closed; slots = %v", slots)
	}
}

func TestAvailability_InvalidDateAndUnknownIdsAreDomainErrors(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Availability(h.ctx, service.AvailabilityQuery{DealershipID: postgres.SeedDealershipID, ServiceTypeID: postgres.SeedServiceTypeOilChangeID, Date: "04/03/2030"})
	expectCode(t, err, domain.CodeValidationError)
	_, err = h.svc.Availability(h.ctx, service.AvailabilityQuery{DealershipID: postgres.SeedDealershipID, ServiceTypeID: "00000000-0000-4000-8000-00000000dead", Date: "2030-03-04"})
	expectCode(t, err, domain.CodeResourceNotFound)
	if !errors.Is(err, err) { // keep errors imported for readers extending this file
		t.Fatal("unreachable")
	}
}
