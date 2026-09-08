package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// Service is the use-case port consumed by the handlers (implemented by
// *service.Scheduler).
type Service interface {
	Availability(ctx context.Context, q service.AvailabilityQuery) (service.AvailabilityResult, error)
	Book(ctx context.Context, r service.BookRequest) (domain.Appointment, error)
	GetAppointment(ctx context.Context, id string) (domain.Appointment, error)
}

// Option customises the router (middleware wiring lives in observability).
type Option func(*routerConfig)

type routerConfig struct {
	middlewares []func(http.Handler) http.Handler
	extraRoutes func(r chi.Router)
	ready       func(ctx context.Context) error
}

// WithReadiness installs a dependency check (e.g. a database ping) behind
// GET /readyz, distinct from the liveness-only /healthz.
func WithReadiness(check func(ctx context.Context) error) Option {
	return func(c *routerConfig) { c.ready = check }
}

// WithMiddleware prepends handler-chain middleware (logging, metrics, tracing).
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(c *routerConfig) { c.middlewares = append(c.middlewares, mw...) }
}

// WithRoutes mounts additional routes (e.g. /metrics) on the root router.
func WithRoutes(fn func(r chi.Router)) Option {
	return func(c *routerConfig) { c.extraRoutes = fn }
}

// NewRouter builds the HTTP API described in requirements.md §10.
func NewRouter(svc Service, opts ...Option) http.Handler {
	cfg := &routerConfig{}
	for _, o := range opts {
		o(cfg)
	}
	h := &handlers{svc: svc}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(cfg.middlewares...)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no such route", nil, nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed for this route", nil, nil)
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if cfg.ready != nil {
			ctx, cancel := context.WithTimeout(req.Context(), time.Second)
			defer cancel()
			if err := cfg.ready(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": "dependency check failed"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	if cfg.extraRoutes != nil {
		cfg.extraRoutes(r)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/availability", h.availability)
		r.Post("/appointments", h.createAppointment)
		r.Get("/appointments/{id}", h.getAppointment)
	})
	return r
}
