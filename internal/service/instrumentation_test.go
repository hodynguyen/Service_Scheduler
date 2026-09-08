package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/repository/postgres"
	"github.com/hodynguyen/service-scheduler/internal/service"
	"github.com/hodynguyen/service-scheduler/internal/testutil/pgtest"
)

type recordingInstrumentation struct {
	mu       sync.Mutex
	started  int
	finished []error
	elapsed  []time.Duration
}

func (r *recordingInstrumentation) BookingStarted(context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started++
}

func (r *recordingInstrumentation) BookingFinished(_ context.Context, err error, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, err)
	r.elapsed = append(r.elapsed, d)
}

func TestBooking_InstrumentationSeesEveryAttemptWithItsOutcome(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Reset(t, pool)
	rec := &recordingInstrumentation{}
	svc := service.New(postgres.New(pool),
		service.WithClock(func() time.Time { return local(monday, 7, 0) }),
		service.WithInstrumentation(rec))
	ctx := context.Background()
	book := func(vehicle string, start time.Time) error {
		_, err := svc.Book(ctx, service.BookRequest{DealershipID: postgres.SeedDealershipID, VehicleID: vehicle, ServiceTypeID: postgres.SeedServiceTypeEVDiagnosticID, StartTime: start})
		return err
	}
	_ = book(postgres.SeedVehicleVF8ID, local(monday, 9, 0))   // confirmed
	_ = book(postgres.SeedVehicleCamryID, local(monday, 9, 0)) // NO_AVAILABLE_RESOURCE
	_ = book(postgres.SeedVehicleCamryID, local(monday, 6, 0)) // START_TIME_IN_PAST -> counted as an attempt too

	if rec.started != 3 || len(rec.finished) != 3 {
		t.Fatalf("started = %d, finished = %d", rec.started, len(rec.finished))
	}
	if rec.finished[0] != nil || domain.CodeOf(rec.finished[1]) != domain.CodeNoAvailableResource || domain.CodeOf(rec.finished[2]) != domain.CodeStartTimeInPast {
		t.Fatalf("outcomes = %v", rec.finished)
	}
	for i, d := range rec.elapsed {
		if d <= 0 {
			t.Fatalf("elapsed[%d] = %v", i, d)
		}
	}
}
