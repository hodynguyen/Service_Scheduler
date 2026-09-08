// Package httpapi exposes the REST API (chi). Handlers validate syntax, call
// the service and map domain errors to the status codes in
// docs/requirements.md §10. No business logic lives here.
package httpapi
