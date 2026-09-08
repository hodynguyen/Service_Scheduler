package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/hodynguyen/service-scheduler/internal/domain"
)

// errorBody is the wire form of every error response.
type errorBody struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	Conflicting []string          `json:"conflicting,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

// statusFor maps requirements.md §10 codes to HTTP statuses.
func statusFor(code domain.Code) int {
	switch code {
	case domain.CodeNoAvailableResource, domain.CodeVehicleAlreadyBooked:
		return http.StatusConflict
	case domain.CodeOutsideBusinessHours, domain.CodeServiceExceedsClosingTime, domain.CodeStartTimeInPast, domain.CodeIdempotencyKeyReused:
		return http.StatusUnprocessableEntity
	case domain.CodeResourceNotFound:
		return http.StatusNotFound
	case domain.CodeValidationError:
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// writeDomainError renders a service error. Non-domain errors become an
// opaque 500; the cause is logged, never returned.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	if de, ok := domain.AsError(err); ok {
		var conflicting []string
		for _, c := range de.Conflicting {
			conflicting = append(conflicting, string(c))
		}
		writeError(w, statusFor(de.Code), string(de.Code), de.Message, conflicting, nil)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		slog.WarnContext(r.Context(), "request aborted", "error", err)
		writeError(w, http.StatusGatewayTimeout, "TIMEOUT", "the request could not be completed in time", nil, nil)
		return
	}
	slog.ErrorContext(r.Context(), "unhandled error", "error", err, "method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred", nil, nil)
}

func writeValidationError(w http.ResponseWriter, message string, details map[string]string) {
	writeError(w, http.StatusBadRequest, string(domain.CodeValidationError), message, nil, details)
}

func writeError(w http.ResponseWriter, status int, code, message string, conflicting []string, details map[string]string) {
	writeJSON(w, status, errorBody{Code: code, Message: message, Conflicting: conflicting, Details: details})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
