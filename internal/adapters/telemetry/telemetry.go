// Package telemetry wires Prometheus metrics and OpenTelemetry tracing.
package telemetry

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var (
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "leafwise_http_request_duration_seconds",
		Help:    "HTTP request latency by route and status.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method", "status"})
	Diagnoses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "leafwise_diagnoses_total",
		Help: "Photo diagnoses by provider and primary condition.",
	}, []string{"provider", "condition"})
	DiagnosisErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "leafwise_diagnosis_errors_total",
		Help: "Failed diagnoses by reason.",
	}, []string{"reason"})
	RemindersSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "leafwise_reminders_sent_total",
		Help: "Care reminders delivered.",
	})
	ReminderRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "leafwise_reminder_runs_total",
		Help: "Reminder job executions by result.",
	}, []string{"result"})
)

// InitTracing configures OTLP/HTTP export when OTEL_EXPORTER_OTLP_ENDPOINT is
// set (the exporter reads the standard OTEL_* env vars). Otherwise tracing is
// a no-op but context propagation still works.
func InitTracing(ctx context.Context, service, version, endpoint string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service), semconv.ServiceVersion(version)))
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Metrics records request latency using the matched route pattern, which
// keeps label cardinality bounded.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		HTTPDuration.WithLabelValues(route, r.Method, strconv.Itoa(sw.status)).Observe(time.Since(start).Seconds())
	})
}
