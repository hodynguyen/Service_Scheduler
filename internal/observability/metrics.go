package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/service"
)

// Metrics owns the Prometheus instruments required by requirements.md §11:
// bookings attempted / confirmed / rejected by reason, a latency histogram on
// the booking path, and per-route HTTP latency.
type Metrics struct {
	registry        *prometheus.Registry
	attempted       prometheus.Counter
	confirmed       prometheus.Counter
	rejected        *prometheus.CounterVec
	bookingDuration prometheus.Histogram
	httpDuration    *prometheus.HistogramVec
	httpInFlight    prometheus.Gauge
}

var _ service.Instrumentation = (*Metrics)(nil)

// NewMetrics registers the instruments on reg (a fresh registry when nil).
func NewMetrics(reg *prometheus.Registry) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
		reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}
	m := &Metrics{
		registry: reg,
		attempted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "scheduler_bookings_attempted_total", Help: "Booking requests that reached the service.",
		}),
		confirmed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "scheduler_bookings_confirmed_total", Help: "Bookings that created a CONFIRMED appointment.",
		}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scheduler_bookings_rejected_total", Help: "Bookings rejected, by domain error code.",
		}, []string{"reason"}),
		bookingDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "scheduler_booking_duration_seconds",
			Help:    "Latency of the booking use case (lookups, selection, transaction).",
			Buckets: []float64{.005, .01, .025, .05, .1, .2, .5, 1, 2.5},
		}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "HTTP request latency by route pattern, method and status.",
			Buckets: []float64{.005, .01, .025, .05, .1, .2, .5, 1, 2.5, 5},
		}, []string{"method", "route", "status"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_server_requests_in_flight", Help: "Requests currently being served.",
		}),
	}
	reg.MustRegister(m.attempted, m.confirmed, m.rejected, m.bookingDuration, m.httpDuration, m.httpInFlight)
	return m
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// BookingStarted implements service.Instrumentation.
func (m *Metrics) BookingStarted(context.Context) { m.attempted.Inc() }

// BookingFinished implements service.Instrumentation.
func (m *Metrics) BookingFinished(_ context.Context, err error, elapsed time.Duration) {
	m.bookingDuration.Observe(elapsed.Seconds())
	switch {
	case err == nil:
		m.confirmed.Inc()
	case domain.CodeOf(err) != "":
		m.rejected.WithLabelValues(string(domain.CodeOf(err))).Inc()
	default:
		m.rejected.WithLabelValues("INTERNAL_ERROR").Inc()
	}
}

// HTTPMiddleware records latency per chi route pattern. The pattern is read
// after the handler ran so parameters like {id} do not explode cardinality.
func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.httpInFlight.Inc()
		defer m.httpInFlight.Dec()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if p := rc.RoutePattern(); p != "" {
				route = p
			}
		}
		m.httpDuration.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Observe(time.Since(start).Seconds())
	})
}
