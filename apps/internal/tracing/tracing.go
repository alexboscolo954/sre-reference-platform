// Package tracing sets up the global OpenTelemetry tracer to export spans over
// OTLP/gRPC to the endpoint in OTEL_EXPORTER_OTLP_ENDPOINT. Sampling happens in
// the collector (tail sampling), so every span is recorded here.
package tracing

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Init installs the tracer provider and W3C trace-context propagation.
// ponytail: no shutdown hook, so spans still buffered at SIGTERM are lost; add one if that matters.
func Init(ctx context.Context, service string) {
	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		log.Fatal(err)
	}
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))),
	))
	otel.SetTextMapPropagator(propagation.TraceContext{})
}
