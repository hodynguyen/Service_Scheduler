package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

const (
	maxBodyBytes         = 64 << 10
	idempotencyKeyHeader = "Idempotency-Key"
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

type handlers struct {
	svc Service
}

// availability handles GET /api/v1/availability (FR-1).
func (h *handlers) availability(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	details := map[string]string{}
	requireUUID(details, "dealershipId", q.Get("dealershipId"))
	requireUUID(details, "serviceTypeId", q.Get("serviceTypeId"))
	date := q.Get("date")
	switch {
	case date == "":
		details["date"] = "required"
	case !dateRe.MatchString(date):
		details["date"] = "must be YYYY-MM-DD"
	}
	requireUUID(details, "vehicleId", q.Get("vehicleId"))
	if len(details) > 0 {
		writeValidationError(w, "invalid query parameters", details)
		return
	}

	res, err := h.svc.Availability(r.Context(), service.AvailabilityQuery{
		DealershipID:  strings.ToLower(q.Get("dealershipId")),
		ServiceTypeID: strings.ToLower(q.Get("serviceTypeId")),
		Date:          date,
		VehicleID:     strings.ToLower(q.Get("vehicleId")),
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAvailabilityResponse(res))
}

// createAppointment handles POST /api/v1/appointments (FR-2, FR-4).
func (h *handlers) createAppointment(w http.ResponseWriter, r *http.Request) {
	var body createAppointmentRequest
	if msg, ok := decodeJSON(w, r, &body); !ok {
		writeValidationError(w, msg, nil)
		return
	}
	details := map[string]string{}
	requireUUID(details, "dealershipId", body.DealershipID)
	requireUUID(details, "vehicleId", body.VehicleID)
	requireUUID(details, "serviceTypeId", body.ServiceTypeID)
	var start time.Time
	switch body.StartTime {
	case "":
		details["startTime"] = "required"
	default:
		t, err := time.Parse(time.RFC3339, body.StartTime)
		if err != nil {
			details["startTime"] = "must be ISO-8601 with an explicit offset, e.g. 2026-09-15T09:00:00+07:00"
		}
		start = t
	}
	key := r.Header.Get(idempotencyKeyHeader)
	if key != "" && !uuidRe.MatchString(key) {
		details[idempotencyKeyHeader] = "must be a UUID"
	}
	if len(details) > 0 {
		writeValidationError(w, "invalid request", details)
		return
	}

	appt, err := h.svc.Book(r.Context(), service.BookRequest{
		DealershipID:   strings.ToLower(body.DealershipID),
		VehicleID:      strings.ToLower(body.VehicleID),
		ServiceTypeID:  strings.ToLower(body.ServiceTypeID),
		StartTime:      start,
		IdempotencyKey: strings.ToLower(key),
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/appointments/"+appt.ID)
	writeJSON(w, http.StatusCreated, toAppointmentResponse(appt))
}

// getAppointment handles GET /api/v1/appointments/{id} (FR-3). A malformed
// id cannot name an appointment, so it is 404 like an unknown one.
func (h *handlers) getAppointment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !uuidRe.MatchString(id) {
		writeDomainError(w, r, domain.NotFound("appointment", id))
		return
	}
	appt, err := h.svc.GetAppointment(r.Context(), strings.ToLower(id))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAppointmentResponse(appt))
}

func requireUUID(details map[string]string, field, value string) {
	switch {
	case value == "":
		details[field] = "required"
	case !uuidRe.MatchString(value):
		details[field] = "must be a UUID"
	}
}

// decodeJSON reads a single JSON object, rejecting unknown fields so a client
// cannot believe it supplied a duration or a technician (BR-8, FR-2).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return "request body is required", false
		case errors.As(err, &syntaxErr):
			return "malformed JSON", false
		case errors.As(err, &typeErr):
			return "field " + typeErr.Field + " has the wrong type", false
		case errors.As(err, &maxErr):
			return "request body too large", false
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			return "unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field "), false
		default:
			return "malformed request body", false
		}
	}
	if dec.More() {
		return "request body must contain a single JSON object", false
	}
	return "", true
}
