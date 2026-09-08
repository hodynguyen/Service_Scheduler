package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hodynguyen/service-scheduler/internal/httpapi"
	"github.com/hodynguyen/service-scheduler/internal/repository/postgres"
	"github.com/hodynguyen/service-scheduler/internal/service"
	"github.com/hodynguyen/service-scheduler/internal/testutil/pgtest"
)

// End-to-end: router -> service -> repository -> Postgres (testcontainers).
func newRealServer(t *testing.T) http.Handler {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.Reset(t, pool)
	fixedNow := time.Date(2030, time.March, 4, 7, 0, 0, 0, hcm)
	svc := service.New(postgres.New(pool), service.WithClock(func() time.Time { return fixedNow }))
	return httpapi.NewRouter(svc)
}

func bookingBody(vehicle, serviceType, start string) string {
	return `{"dealershipId":"` + postgres.SeedDealershipID + `","vehicleId":"` + vehicle + `","serviceTypeId":"` + serviceType + `","startTime":"` + start + `"}`
}

func TestE2E_AC01_AC02_AC03_BookThenFetch(t *testing.T) {
	h := newRealServer(t)
	rec, body := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-04T09:00:00+07:00"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if body["endTime"] != "2030-03-04T10:00:00+07:00" {
		t.Errorf("endTime = %v (AC-02)", body["endTime"])
	}
	if body["customer"].(map[string]any)["name"] != "Mai Pham" {
		t.Errorf("customer = %v (AC-03)", body["customer"])
	}
	if body["technician"].(map[string]any)["name"] != "An Nguyen" || body["bay"].(map[string]any)["name"] != "Alignment rig" {
		t.Errorf("assignment = %v / %v (AC-01)", body["technician"], body["bay"])
	}
	got, fetched := do(t, h, http.MethodGet, rec.Header().Get("Location"), "")
	if got.Code != http.StatusOK || fetched["id"] != body["id"] || got.Body.String() != rec.Body.String() {
		t.Fatalf("GET = %d %s", got.Code, got.Body.String())
	}
}

func TestE2E_UTCInputIsRenderedInDealershipOffset(t *testing.T) {
	h := newRealServer(t)
	rec, body := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeOilChangeID, "2030-03-04T02:00:00Z"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if body["startTime"] != "2030-03-04T09:00:00+07:00" {
		t.Fatalf("startTime = %v, want the dealership's +07:00 rendering", body["startTime"])
	}
}

func TestE2E_AC06_ConflictCarriesBothResources(t *testing.T) {
	h := newRealServer(t)
	first, _ := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleVF8ID, postgres.SeedServiceTypeEVDiagnosticID, "2030-03-04T09:00:00+07:00"))
	if first.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", first.Code, first.Body.String())
	}
	rec, body := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeEVDiagnosticID, "2030-03-04T09:30:00+07:00"))
	expectError(t, rec, body, http.StatusConflict, "NO_AVAILABLE_RESOURCE")
	if c := body["conflicting"].([]any); len(c) != 2 || c[0] != "BAY" || c[1] != "TECHNICIAN" {
		t.Fatalf("conflicting = %v", body["conflicting"])
	}
}

func TestE2E_AC14_AC16_TimeRulesSurfaceAs422(t *testing.T) {
	h := newRealServer(t)
	rec, body := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-04T16:30:00+07:00"))
	expectError(t, rec, body, http.StatusUnprocessableEntity, "SERVICE_EXCEEDS_CLOSING_TIME")
	rec, body = do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-04T06:00:00+07:00"))
	expectError(t, rec, body, http.StatusUnprocessableEntity, "START_TIME_IN_PAST")
	rec, body = do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-10T09:00:00+07:00"))
	expectError(t, rec, body, http.StatusUnprocessableEntity, "OUTSIDE_BUSINESS_HOURS")
}

func TestE2E_AC19_IdempotencyKeyHeaderReplaysThe201(t *testing.T) {
	h := newRealServer(t)
	key := "8d2c8a52-1b1e-4c65-9b26-0000000000e2"
	body := bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-04T09:00:00+07:00")
	first, _ := do(t, h, http.MethodPost, "/api/v1/appointments", body, "Idempotency-Key", key)
	second, _ := do(t, h, http.MethodPost, "/api/v1/appointments", body, "Idempotency-Key", key)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("statuses = %d, %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay must return the original body:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	// Same key, different payload.
	rec, errBody := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-04T11:00:00+07:00"), "Idempotency-Key", key)
	expectError(t, rec, errBody, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED")
	// Without the header the same payload is a genuine second attempt: the vehicle is now busy.
	rec, errBody = do(t, h, http.MethodPost, "/api/v1/appointments", body)
	expectError(t, rec, errBody, http.StatusConflict, "VEHICLE_ALREADY_BOOKED")
}

func TestE2E_AC22_AvailabilityReflectsBookings(t *testing.T) {
	h := newRealServer(t)
	url := "/api/v1/availability?dealershipId=" + postgres.SeedDealershipID + "&serviceTypeId=" + postgres.SeedServiceTypeAlignmentID + "&date=2030-03-04"
	rec, before := do(t, h, http.MethodGet, url, "")
	if rec.Code != http.StatusOK || len(before["availableSlots"].([]any)) != 17 {
		t.Fatalf("before: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/v1/appointments", bookingBody(postgres.SeedVehicleCamryID, postgres.SeedServiceTypeAlignmentID, "2030-03-04T10:00:00+07:00")); rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d", rec.Code)
	}
	rec, after := do(t, h, http.MethodGet, url, "")
	slots := after["availableSlots"].([]any)
	if len(slots) != 14 {
		t.Fatalf("after: %d slots %v", len(slots), slots)
	}
	if strings.Contains(rec.Body.String(), "T10:00:00+07:00") || strings.Contains(rec.Body.String(), "T09:30:00+07:00") || strings.Contains(rec.Body.String(), "T10:30:00+07:00") {
		t.Fatalf("overlapping slots must be excluded: %s", rec.Body.String())
	}
}
