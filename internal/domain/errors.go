package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Code is the machine-readable error code from requirements.md §10.
type Code string

const (
	CodeNoAvailableResource       Code = "NO_AVAILABLE_RESOURCE"
	CodeVehicleAlreadyBooked      Code = "VEHICLE_ALREADY_BOOKED"
	CodeOutsideBusinessHours      Code = "OUTSIDE_BUSINESS_HOURS"
	CodeServiceExceedsClosingTime Code = "SERVICE_EXCEEDS_CLOSING_TIME"
	CodeStartTimeInPast           Code = "START_TIME_IN_PAST"
	CodeResourceNotFound          Code = "RESOURCE_NOT_FOUND"
	CodeValidationError           Code = "VALIDATION_ERROR"
	// CodeIdempotencyKeyReused is not in §10: it covers a key replayed with a
	// different payload, which the specification does not address.
	CodeIdempotencyKeyReused Code = "IDEMPOTENCY_KEY_REUSED"
	// CodeContention marks a retryable failure: the request repeatedly lost
	// races for resources that are still free. Unlike NO_AVAILABLE_RESOURCE it
	// asserts nothing about which resource is scarce, because nothing is.
	CodeContention Code = "CONTENTION"
)

// ResourceKind names what could not be allocated (the `conflicting` array).
type ResourceKind string

const (
	ResourceBay        ResourceKind = "BAY"
	ResourceTechnician ResourceKind = "TECHNICIAN"
)

// Error is the only error type that crosses the service boundary.
type Error struct {
	Code        Code
	Message     string
	Conflicting []ResourceKind // set only for CodeNoAvailableResource
}

func (e *Error) Error() string {
	if len(e.Conflicting) == 0 {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	parts := make([]string, len(e.Conflicting))
	for i, c := range e.Conflicting {
		parts[i] = string(c)
	}
	return fmt.Sprintf("%s: %s (conflicting: %s)", e.Code, e.Message, strings.Join(parts, ","))
}

// NewError builds a domain error.
func NewError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Contention builds the retryable contention error. The cause — SQLSTATE,
// constraint name, which competitor won — belongs in logs and span attributes,
// never in a client-facing message, so the text is fixed here.
func Contention() *Error {
	return NewError(CodeContention, "the request could not be scheduled due to concurrent contention; retry")
}

// NotFound reports an unknown dealership, vehicle, service type or appointment.
func NotFound(what, id string) *Error {
	return NewError(CodeResourceNotFound, "%s %q not found", what, id)
}

// AsError unwraps a *Error from err.
func AsError(err error) (*Error, bool) {
	var de *Error
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}

// CodeOf returns the domain code of err, or "" for non-domain errors.
func CodeOf(err error) Code {
	if de, ok := AsError(err); ok {
		return de.Code
	}
	return ""
}
