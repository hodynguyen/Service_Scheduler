package observability

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// SetPropagators installs W3C trace-context + baggage propagation.
func SetPropagators() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
}

// SetupTracing installs a global TracerProvider. With an OTLP/HTTP endpoint
// (OTEL_EXPORTER_OTLP_ENDPOINT) spans are exported in batches; without one
// spans are still created (so trace ids appear in logs) but dropped.
func SetupTracing(ctx context.Context, serviceName, otlpEndpoint string) (func(context.Context) error, error) {
	SetPropagators()
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if otlpEndpoint != "" {
		exporter, err := otlptracehttp.New(ctx) // endpoint read from OTEL_EXPORTER_OTLP_ENDPOINT
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}
	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

var httpTracer = otel.Tracer("github.com/hodynguyen/service-scheduler/internal/observability")

// TracingMiddleware starts a SERVER span per request, named "<METHOD> <route>"
// once chi has matched the route, and records the response status.
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := httpTracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.URLPath(r.URL.Path),
				semconv.UserAgentOriginal(r.UserAgent()),
			),
		)
		defer span.End()

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(ctx))

		if rc := chi.RouteContext(r.Context()); rc != nil {
			if p := rc.RoutePattern(); p != "" {
				span.SetName(r.Method + " " + p)
				span.SetAttributes(semconv.HTTPRoute(p))
			}
		}
		span.SetAttributes(semconv.HTTPResponseStatusCode(sw.status))
		if id := CorrelationID(ctx); id != "" {
			span.SetAttributes(attribute.String("correlation_id", id))
		}
		if sw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(sw.status))
		}
	})
}
