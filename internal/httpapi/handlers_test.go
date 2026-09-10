package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/httpapi"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// fakeService records calls and returns canned results so handler behaviour
// (validation, status codes, JSON shape) is tested without a database.
type fakeService struct {
	availability func(service.AvailabilityQuery) (service.AvailabilityResult, error)
	book         func(service.BookRequest) (domain.Appointment, error)
	get          func(string) (domain.Appointment, error)

	lastBook         service.BookRequest
	lastAvailability service.AvailabilityQuery
	lastGet          string
}

func (f *fakeService) Availability(_ context.Context, q service.AvailabilityQuery) (service.AvailabilityResult, error) {
	f.lastAvailability = q
	return f.availability(q)
}

func (f *fakeService) Book(_ context.Context, r service.BookRequest) (domain.Appointment, error) {
	f.lastBook = r
	return f.book(r)
}

func (f *fakeService) GetAppointment(_ context.Context, id string) (domain.Appointment, error) {
	f.lastGet = id
	return f.get(id)
}

var hcm = func() *time.Location {
	l, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		panic(err)
	}
	return l
}()

const (
	dealershipID  = "10000000-0000-4000-8000-000000000001"
	vehicleID     = "70000000-0000-4000-8000-000000000001"
	serviceTypeID = "30000000-0000-4000-8000-000000000002"
	appointmentID = "a0000000-0000-4000-8000-000000000001"
)

func sampleAppointment() domain.Appointment {
	start := time.Date(2030, time.March, 4, 9, 0, 0, 0, hcm)
	return domain.Appointment{
		ID:           appointmentID,
		DealershipID: dealershipID,
		Status:       domain.StatusConfirmed,
		Interval:     domain.NewInterval(start, time.Hour),
		Customer:     domain.Customer{ID: "60000000-0000-4000-8000-000000000001", Name: "Mai Pham"},
		Vehicle:      domain.Vehicle{ID: vehicleID, VIN: "JTNB11HK7J3000001", Model: "Toyota Camry 2.5Q"},
		ServiceType:  domain.ServiceType{ID: serviceTypeID, Name: "Wheel alignment and balancing", Duration: time.Hour},
		Technician:   domain.Technician{ID: "50000000-0000-4000-8000-000000000001", Name: "An Nguyen"},
		Bay:          domain.Bay{ID: "40000000-0000-4000-8000-000000000003", Name: "Alignment rig", Type: domain.BayTypeAlignment},
	}
}

func newServer(f *fakeService) http.Handler { return httpapi.NewRouter(f) }

func do(t *testing.T, h http.Handler, method, target, body string, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var parsed map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("response is not a JSON object: %v\n%s", err, rec.Body.String())
		}
	}
	return rec, parsed
}

const validBody = `{"dealershipId":"` + dealershipID + `","vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `","startTime":"2030-03-04T09:00:00+07:00"}`

func expectError(t *testing.T, rec *httptest.ResponseRecorder, body map[string]any, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	if body["code"] != code {
		t.Fatalf("code = %v, want %s; body %s", body["code"], code, rec.Body.String())
	}
	if msg, _ := body["message"].(string); msg == "" {
		t.Fatalf("error responses carry a human-readable message; body %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

// --- POST /api/v1/appointments -------------------------------------------

func TestHTTP_AC01_PostAppointmentReturns201WithSpecBody(t *testing.T) {
	f := &fakeService{book: func(service.BookRequest) (domain.Appointment, error) { return sampleAppointment(), nil }}
	rec, body := do(t, newServer(f), http.MethodPost, "/api/v1/appointments", validBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/appointments/"+appointmentID {
		t.Fatalf("Location = %q", loc)
	}
	want := map[string]any{
		"id":        appointmentID,
		"status":    "CONFIRMED",
		"startTime": "2030-03-04T09:00:00+07:00",
		"endTime":   "2030-03-04T10:00:00+07:00",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	nested := map[string][]string{
		"customer":    {"id", "name"},
		"vehicle":     {"id", "vin", "model"},
		"serviceType": {"id", "name"},
		"technician":  {"id", "name"},
		"bay":         {"id", "name", "bayType"},
	}
	for obj, keys := range nested {
		m, ok := body[obj].(map[string]any)
		if !ok {
			t.Fatalf("%s missing or not an object: %v", obj, body[obj])
		}
		for _, k := range keys {
			if s, _ := m[k].(string); s == "" {
				t.Errorf("%s.%s missing", obj, k)
			}
		}
		if len(m) != len(keys) {
			t.Errorf("%s has extra fields: %v (spec lists %v)", obj, m, keys)
		}
	}
	if body["bay"].(map[string]any)["bayType"] != "ALIGNMENT" {
		t.Errorf("bay.bayType = %v", body["bay"])
	}
	// Request mapping: the service receives the parsed instant and no key.
	if f.lastBook.DealershipID != dealershipID || f.lastBook.VehicleID != vehicleID || f.lastBook.ServiceTypeID != serviceTypeID {
		t.Fatalf("service received %+v", f.lastBook)
	}
	if !f.lastBook.StartTime.Equal(time.Date(2030, time.March, 4, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("startTime = %v", f.lastBook.StartTime)
	}
	if f.lastBook.IdempotencyKey != "" {
		t.Fatalf("no Idempotency-Key was sent, got %q", f.lastBook.IdempotencyKey)
	}
}

func TestHTTP_AC19_IdempotencyKeyHeaderIsForwarded(t *testing.T) {
	f := &fakeService{book: func(service.BookRequest) (domain.Appointment, error) { return sampleAppointment(), nil }}
	key := "8d2c8a52-1b1e-4c65-9b26-000000000001"
	rec, _ := do(t, newServer(f), http.MethodPost, "/api/v1/appointments", validBody, "Idempotency-Key", key)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if f.lastBook.IdempotencyKey != key {
		t.Fatalf("key = %q, want %q", f.lastBook.IdempotencyKey, key)
	}
}

func TestHTTP_PostAppointmentValidationErrors(t *testing.T) {
	f := &fakeService{book: func(service.BookRequest) (domain.Appointment, error) {
		t.Fatal("service must not be called on a malformed request")
		return domain.Appointment{}, nil
	}}
	h := newServer(f)
	cases := map[string]struct {
		body    string
		headers []string
	}{
		"malformed json":       {body: `{"dealershipId":`},
		"empty body":           {body: ``},
		"json array":           {body: `[]`},
		"missing dealershipId": {body: `{"vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `","startTime":"2030-03-04T09:00:00+07:00"}`},
		"missing startTime":    {body: `{"dealershipId":"` + dealershipID + `","vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `"}`},
		"bad uuid":             {body: `{"dealershipId":"not-a-uuid","vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `","startTime":"2030-03-04T09:00:00+07:00"}`},
		"startTime no offset":  {body: `{"dealershipId":"` + dealershipID + `","vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `","startTime":"2030-03-04T09:00:00"}`},
		"startTime date only":  {body: `{"dealershipId":"` + dealershipID + `","vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `","startTime":"2030-03-04"}`},
		"unknown field (BR-8)": {body: `{"dealershipId":"` + dealershipID + `","vehicleId":"` + vehicleID + `","serviceTypeId":"` + serviceTypeID + `","startTime":"2030-03-04T09:00:00+07:00","durationMinutes":15}`},
		"bad idempotency key":  {body: validBody, headers: []string{"Idempotency-Key", "not-a-uuid"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec, body := do(t, h, http.MethodPost, "/api/v1/appointments", tc.body, tc.headers...)
			expectError(t, rec, body, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestHTTP_DomainErrorsMapToSpecStatusCodes(t *testing.T) {
	cases := []struct {
		err         *domain.Error
		status      int
		conflicting []any
	}{
		{&domain.Error{Code: domain.CodeNoAvailableResource, Message: "x", Conflicting: []domain.ResourceKind{domain.ResourceBay}}, http.StatusConflict, []any{"BAY"}},
		{&domain.Error{Code: domain.CodeNoAvailableResource, Message: "x", Conflicting: []domain.ResourceKind{domain.ResourceBay, domain.ResourceTechnician}}, http.StatusConflict, []any{"BAY", "TECHNICIAN"}},
		{domain.NewError(domain.CodeVehicleAlreadyBooked, "x"), http.StatusConflict, nil},
		{domain.NewError(domain.CodeOutsideBusinessHours, "x"), http.StatusUnprocessableEntity, nil},
		{domain.NewError(domain.CodeServiceExceedsClosingTime, "x"), http.StatusUnprocessableEntity, nil},
		{domain.NewError(domain.CodeStartTimeInPast, "x"), http.StatusUnprocessableEntity, nil},
		{domain.NewError(domain.CodeIdempotencyKeyReused, "x"), http.StatusUnprocessableEntity, nil},
		{domain.NewError(domain.CodeContention, "x"), http.StatusServiceUnavailable, nil},
		{domain.NotFound("vehicle", vehicleID), http.StatusNotFound, nil},
		{domain.NewError(domain.CodeValidationError, "x"), http.StatusBadRequest, nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.err.Code), func(t *testing.T) {
			f := &fakeService{book: func(service.BookRequest) (domain.Appointment, error) { return domain.Appointment{}, tc.err }}
			rec, body := do(t, newServer(f), http.MethodPost, "/api/v1/appointments", validBody)
			expectError(t, rec, body, tc.status, string(tc.err.Code))
			got, present := body["conflicting"]
			if tc.conflicting == nil {
				if present {
					t.Fatalf("conflicting must be omitted, got %v", got)
				}
				return
			}
			arr, _ := got.([]any)
			if len(arr) != len(tc.conflicting) {
				t.Fatalf("conflicting = %v, want %v", got, tc.conflicting)
			}
			for i := range arr {
				if arr[i] != tc.conflicting[i] {
					t.Fatalf("conflicting = %v, want %v", got, tc.conflicting)
				}
			}
		})
	}
}

func TestHTTP_UnexpectedErrorIs500WithoutLeakingDetails(t *testing.T) {
	f := &fakeService{book: func(service.BookRequest) (domain.Appointment, error) {
		return domain.Appointment{}, errors.New("pq: connection refused to 10.0.0.7")
	}}
	rec, body := do(t, newServer(f), http.MethodPost, "/api/v1/appointments", validBody)
	expectError(t, rec, body, http.StatusInternalServerError, "INTERNAL_ERROR")
	if strings.Contains(rec.Body.String(), "10.0.0.7") {
		t.Fatal("internal details must not leak")
	}
}

// --- GET /api/v1/availability --------------------------------------------

func TestHTTP_AC24_AvailabilityRendersSpecBodyAndEmptyArray(t *testing.T) {
	f := &fakeService{availability: func(q service.AvailabilityQuery) (service.AvailabilityResult, error) {
		return service.AvailabilityResult{
			Date:        domain.Date{Year: 2030, Month: time.March, Day: 4},
			ServiceType: domain.ServiceType{ID: serviceTypeID, Name: "Wheel alignment and balancing", Duration: time.Hour},
			Slots:       []time.Time{},
		}, nil
	}}
	rec, body := do(t, newServer(f), http.MethodGet, "/api/v1/availability?dealershipId="+dealershipID+"&serviceTypeId="+serviceTypeID+"&date=2030-03-04&vehicleId="+vehicleID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if body["date"] != "2030-03-04" {
		t.Errorf("date = %v", body["date"])
	}
	st := body["serviceType"].(map[string]any)
	if st["id"] != serviceTypeID || st["name"] == "" || st["durationMinutes"] != float64(60) {
		t.Errorf("serviceType = %v", st)
	}
	if !strings.Contains(rec.Body.String(), `"availableSlots":[]`) {
		t.Errorf("empty slots must serialise as [] not null: %s", rec.Body.String())
	}
	if f.lastAvailability.DealershipID != dealershipID || f.lastAvailability.ServiceTypeID != serviceTypeID || f.lastAvailability.Date != "2030-03-04" || f.lastAvailability.VehicleID != vehicleID {
		t.Fatalf("service received %+v", f.lastAvailability)
	}
}

func TestHTTP_AvailabilitySlotsCarryDealershipOffset(t *testing.T) {
	f := &fakeService{availability: func(q service.AvailabilityQuery) (service.AvailabilityResult, error) {
		return service.AvailabilityResult{
			Date:        domain.Date{Year: 2030, Month: time.March, Day: 4},
			ServiceType: domain.ServiceType{ID: serviceTypeID, Name: "x", Duration: time.Hour},
			Slots:       []time.Time{time.Date(2030, time.March, 4, 9, 0, 0, 0, hcm), time.Date(2030, time.March, 4, 10, 30, 0, 0, hcm)},
		}, nil
	}}
	rec, body := do(t, newServer(f), http.MethodGet, "/api/v1/availability?dealershipId="+dealershipID+"&serviceTypeId="+serviceTypeID+"&date=2030-03-04&vehicleId="+vehicleID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	slots := body["availableSlots"].([]any)
	if len(slots) != 2 || slots[0] != "2030-03-04T09:00:00+07:00" || slots[1] != "2030-03-04T10:30:00+07:00" {
		t.Fatalf("availableSlots = %v", slots)
	}
	if f.lastAvailability.VehicleID != vehicleID {
		t.Fatalf("vehicleId must be forwarded, got %q", f.lastAvailability.VehicleID)
	}
}

func TestHTTP_AvailabilityQueryValidation(t *testing.T) {
	f := &fakeService{availability: func(service.AvailabilityQuery) (service.AvailabilityResult, error) {
		t.Fatal("service must not be called")
		return service.AvailabilityResult{}, nil
	}}
	h := newServer(f)
	for name, q := range map[string]string{
		"missing dealershipId":  "serviceTypeId=" + serviceTypeID + "&date=2030-03-04&vehicleId=" + vehicleID,
		"missing serviceTypeId": "dealershipId=" + dealershipID + "&date=2030-03-04&vehicleId=" + vehicleID,
		"missing date":          "dealershipId=" + dealershipID + "&serviceTypeId=" + serviceTypeID + "&vehicleId=" + vehicleID,
		"missing vehicleId":     "dealershipId=" + dealershipID + "&serviceTypeId=" + serviceTypeID + "&date=2030-03-04",
		"bad date":              "dealershipId=" + dealershipID + "&serviceTypeId=" + serviceTypeID + "&date=04-03-2030&vehicleId=" + vehicleID,
		"bad uuid":              "dealershipId=nope&serviceTypeId=" + serviceTypeID + "&date=2030-03-04&vehicleId=" + vehicleID,
		"bad vehicleId":         "dealershipId=" + dealershipID + "&serviceTypeId=" + serviceTypeID + "&date=2030-03-04&vehicleId=nope",
	} {
		t.Run(name, func(t *testing.T) {
			rec, body := do(t, h, http.MethodGet, "/api/v1/availability?"+q, "")
			expectError(t, rec, body, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestHTTP_AvailabilityNotFoundIs404(t *testing.T) {
	f := &fakeService{availability: func(service.AvailabilityQuery) (service.AvailabilityResult, error) {
		return service.AvailabilityResult{}, domain.NotFound("dealership", dealershipID)
	}}
	rec, body := do(t, newServer(f), http.MethodGet, "/api/v1/availability?dealershipId="+dealershipID+"&serviceTypeId="+serviceTypeID+"&date=2030-03-04&vehicleId="+vehicleID, "")
	expectError(t, rec, body, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

// --- GET /api/v1/appointments/{id} ---------------------------------------

func TestHTTP_FR3_GetAppointmentReturnsSameBodyAs201(t *testing.T) {
	f := &fakeService{
		book: func(service.BookRequest) (domain.Appointment, error) { return sampleAppointment(), nil },
		get:  func(string) (domain.Appointment, error) { return sampleAppointment(), nil },
	}
	h := newServer(f)
	created, _ := do(t, h, http.MethodPost, "/api/v1/appointments", validBody)
	fetched, _ := do(t, h, http.MethodGet, "/api/v1/appointments/"+appointmentID, "")
	if fetched.Code != http.StatusOK {
		t.Fatalf("status = %d", fetched.Code)
	}
	if created.Body.String() != fetched.Body.String() {
		t.Fatalf("GET body must equal the 201 body:\n%s\n%s", created.Body.String(), fetched.Body.String())
	}
	if f.lastGet != appointmentID {
		t.Fatalf("service received id %q", f.lastGet)
	}
}

func TestHTTP_GetAppointmentUnknownOrMalformedIdIs404(t *testing.T) {
	f := &fakeService{get: func(id string) (domain.Appointment, error) {
		return domain.Appointment{}, domain.NotFound("appointment", id)
	}}
	h := newServer(f)
	rec, body := do(t, h, http.MethodGet, "/api/v1/appointments/"+appointmentID, "")
	expectError(t, rec, body, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	rec, body = do(t, h, http.MethodGet, "/api/v1/appointments/not-a-uuid", "")
	expectError(t, rec, body, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestHTTP_UnknownRouteAndMethod(t *testing.T) {
	f := &fakeService{}
	h := newServer(f)
	rec, _ := do(t, h, http.MethodGet, "/api/v1/nothing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	rec, _ = do(t, h, http.MethodDelete, "/api/v1/appointments/"+appointmentID, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	rec, _ = do(t, h, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec.Code)
	}
}

func TestHTTP_ReadyzReflectsDependencyCheck(t *testing.T) {
	f := &fakeService{}
	ok := httpapi.NewRouter(f, httpapi.WithReadiness(func(context.Context) error { return nil }))
	rec, _ := do(t, ok, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status = %d", rec.Code)
	}
	down := httpapi.NewRouter(f, httpapi.WithReadiness(func(context.Context) error { return errors.New("db down") }))
	rec, body := do(t, down, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("not-ready status = %d body %v (must not leak the cause)", rec.Code, body)
	}
}

func TestHTTP_ContentionIs503WithRetryAfterAndNoInternals(t *testing.T) {
	f := &fakeService{book: func(service.BookRequest) (domain.Appointment, error) {
		return domain.Appointment{}, domain.NewError(domain.CodeContention,
			"the request could not be scheduled due to concurrent contention; retry")
	}}
	rec, body := do(t, newServer(f), http.MethodPost, "/api/v1/appointments", validBody)
	expectError(t, rec, body, http.StatusServiceUnavailable, "CONTENTION")
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
	if _, present := body["conflicting"]; present {
		t.Fatalf("contention names no scarce resource: %v", body["conflicting"])
	}
	for _, banned := range []string{"deadlock", "40P01", "23P01", "sqlstate", "constraint"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), banned) {
			t.Errorf("response body must not contain %q: %s", banned, rec.Body.String())
		}
	}
}
