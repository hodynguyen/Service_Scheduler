package service

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
)

// Instrumentation receives booking outcomes for metrics. Implemented by
// observability.Metrics; the zero value (nil) is a no-op.
type Instrumentation interface {
	BookingStarted(ctx context.Context)
	BookingFinished(ctx context.Context, err error, elapsed time.Duration)
}

// WithInstrumentation attaches a metrics sink.
func WithInstrumentation(i Instrumentation) Option { return func(s *Scheduler) { s.metrics = i } }

type noopInstrumentation struct{}

func (noopInstrumentation) BookingStarted(context.Context)                        {}
func (noopInstrumentation) BookingFinished(context.Context, error, time.Duration) {}

var tracer = otel.Tracer("github.com/hodynguyen/service-scheduler/internal/service")
