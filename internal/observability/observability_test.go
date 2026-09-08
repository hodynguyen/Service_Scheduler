package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/hodynguyen/service-scheduler/internal/domain"
	"github.com/hodynguyen/service-scheduler/internal/observability"
)

// --- Correlation IDs and logging -------------------------------------------

func TestCorrelation_GeneratesIDAndEchoesItInResponse(t *testing.T) {
	var seen string
	h := observability.CorrelationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = observability.CorrelationID(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || len(seen) < 16 {
		t.Fatalf("a correlation id must be generated, got %q", seen)
	}
	if got := rec.Header().Get("X-Correlation-ID"); got != seen {
		t.Fatalf("response header = %q, want %q", got, seen)
	}
}

func TestCorrelation_PropagatesIncomingHeader(t *testing.T) {
	var seen string
	h := observability.CorrelationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = observability.CorrelationID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-ID", "client-supplied-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "client-supplied-123" || rec.Header().Get("X-Correlation-ID") != "client-supplied-123" {
		t.Fatalf("incoming id must be reused, got %q / %q", seen, rec.Header().Get("X-Correlation-ID"))
	}
}

func TestLogger_EmitsJSONWithCorrelationAndTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, slog.LevelInfo)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	ctx = observability.WithCorrelationID(ctx, "corr-42")
	logger.InfoContext(ctx, "hello", "k", "v")
	span.End()

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "hello" || rec["k"] != "v" || rec["level"] != "INFO" {
		t.Fatalf("record = %v", rec)
	}
	if rec["correlation_id"] != "corr-42" {
		t.Fatalf("correlation_id = %v", rec["correlation_id"])
	}
	if rec["trace_id"] != span.SpanContext().TraceID().String() || rec["span_id"] != span.SpanContext().SpanID().String() {
		t.Fatalf("trace ids missing or wrong: %v", rec)
	}
}

func TestRequestLogger_LogsOneLinePerRequestWithStatusAndDuration(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, slog.LevelInfo)
	h := observability.CorrelationMiddleware(observability.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short"))
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/appointments?x=1", nil))
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %s", buf.String())
	}
	if line["method"] != "POST" || line["path"] != "/api/v1/appointments" || line["status"] != float64(418) || line["bytes"] != float64(5) {
		t.Fatalf("line = %v", line)
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Fatalf("duration_ms missing: %v", line)
	}
	if line["correlation_id"] == nil || line["correlation_id"] == "" {
		t.Fatalf("correlation_id missing: %v", line)
	}
}

// --- Metrics ----------------------------------------------------------------

func gather(t *testing.T, reg *prometheus.Registry, name string) []*dto.Metric {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f.GetMetric()
		}
	}
	return nil
}

func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	for _, m := range gather(t, reg, name) {
		match := true
		for k, v := range labels {
			found := false
			for _, lp := range m.GetLabel() {
				if lp.GetName() == k && lp.GetValue() == v {
					found = true
				}
			}
			if !found {
				match = false
			}
		}
		if match {
			return m.GetCounter().GetValue()
		}
	}
	return -1
}

func TestMetrics_BookingCountersByOutcomeAndLatencyHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewMetrics(reg)
	ctx := context.Background()

	m.BookingStarted(ctx)
	m.BookingFinished(ctx, nil, 12*time.Millisecond)
	m.BookingStarted(ctx)
	m.BookingFinished(ctx, &domain.Error{Code: domain.CodeNoAvailableResource, Conflicting: []domain.ResourceKind{domain.ResourceBay}}, 8*time.Millisecond)
	m.BookingStarted(ctx)
	m.BookingFinished(ctx, domain.NewError(domain.CodeVehicleAlreadyBooked, "x"), 3*time.Millisecond)
	m.BookingStarted(ctx)
	m.BookingFinished(ctx, context.DeadlineExceeded, time.Second)

	if got := counterValue(t, reg, "scheduler_bookings_attempted_total", nil); got != 4 {
		t.Fatalf("attempted = %v, want 4", got)
	}
	if got := counterValue(t, reg, "scheduler_bookings_confirmed_total", nil); got != 1 {
		t.Fatalf("confirmed = %v, want 1", got)
	}
	if got := counterValue(t, reg, "scheduler_bookings_rejected_total", map[string]string{"reason": "NO_AVAILABLE_RESOURCE"}); got != 1 {
		t.Fatalf("rejected{NO_AVAILABLE_RESOURCE} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "scheduler_bookings_rejected_total", map[string]string{"reason": "VEHICLE_ALREADY_BOOKED"}); got != 1 {
		t.Fatalf("rejected{VEHICLE_ALREADY_BOOKED} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "scheduler_bookings_rejected_total", map[string]string{"reason": "INTERNAL_ERROR"}); got != 1 {
		t.Fatalf("rejected{INTERNAL_ERROR} = %v, want 1", got)
	}
	hist := gather(t, reg, "scheduler_booking_duration_seconds")
	var samples uint64
	for _, h := range hist {
		samples += h.GetHistogram().GetSampleCount()
	}
	if samples != 4 {
		t.Fatalf("histogram samples = %d, want 4", samples)
	}
}

func TestMetrics_HTTPMiddlewareLabelsByRoutePatternNotRawPath(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewMetrics(reg)
	r := chi.NewRouter()
	r.Use(m.HTTPMiddleware)
	r.Get("/api/v1/appointments/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/appointments/abc-123", nil))

	hist := gather(t, reg, "http_server_request_duration_seconds")
	if len(hist) != 1 {
		t.Fatalf("expected one series, got %d", len(hist))
	}
	labels := map[string]string{}
	for _, lp := range hist[0].GetLabel() {
		labels[lp.GetName()] = lp.GetValue()
	}
	if labels["route"] != "/api/v1/appointments/{id}" || labels["method"] != "GET" || labels["status"] != "404" {
		t.Fatalf("labels = %v", labels)
	}
	if hist[0].GetHistogram().GetSampleCount() != 1 {
		t.Fatalf("sample count = %d", hist[0].GetHistogram().GetSampleCount())
	}

	// /metrics is served from the same registry.
	mrec := httptest.NewRecorder()
	m.Handler().ServeHTTP(mrec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if mrec.Code != http.StatusOK || !strings.Contains(mrec.Body.String(), "http_server_request_duration_seconds") {
		t.Fatalf("/metrics = %d\n%s", mrec.Code, mrec.Body.String())
	}
}

// --- Tracing ----------------------------------------------------------------

func TestTracingMiddleware_CreatesServerSpanNamedByRouteAndPropagatesContext(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	observability.SetPropagators()

	r := chi.NewRouter()
	r.Use(observability.TracingMiddleware)
	r.Post("/api/v1/appointments", func(w http.ResponseWriter, r *http.Request) {
		_, child := otel.Tracer("test").Start(r.Context(), "child")
		child.End()
		w.WriteHeader(http.StatusConflict)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appointments", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	r.ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want server + child", len(spans))
	}
	var server, child tracetest.SpanStub
	for _, s := range spans {
		if s.Name == "child" {
			child = s
		} else {
			server = s
		}
	}
	if server.Name != "POST /api/v1/appointments" {
		t.Fatalf("server span name = %q", server.Name)
	}
	if server.SpanContext.TraceID().String() != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("incoming traceparent must be honoured, got %s", server.SpanContext.TraceID())
	}
	if child.Parent.SpanID() != server.SpanContext.SpanID() {
		t.Fatal("handler spans must be children of the server span")
	}
	var status string
	for _, a := range server.Attributes {
		if string(a.Key) == "http.response.status_code" {
			status = a.Value.Emit()
		}
	}
	if status != "409" {
		t.Fatalf("http.response.status_code = %q", status)
	}
}

func TestSetupTracing_WithoutEndpointInstallsProviderAndShutsDown(t *testing.T) {
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	shutdown, err := observability.SetupTracing(context.Background(), "service-scheduler-test", "")
	if err != nil {
		t.Fatalf("SetupTracing must succeed without an exporter endpoint: %v", err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "op")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
