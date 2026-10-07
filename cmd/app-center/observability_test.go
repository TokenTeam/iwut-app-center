package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type observabilityTestHeader map[string][]string

func (header observabilityTestHeader) Get(key string) string {
	values := header.Values(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
func (header observabilityTestHeader) Set(key, value string) {
	header[strings.ToLower(key)] = []string{value}
}
func (header observabilityTestHeader) Add(key, value string) {
	key = strings.ToLower(key)
	header[key] = append(header[key], value)
}
func (header observabilityTestHeader) Keys() []string {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	return keys
}
func (header observabilityTestHeader) Values(key string) []string {
	return header[strings.ToLower(key)]
}

type observabilityTestTransport struct{ header observabilityTestHeader }

func (observabilityTestTransport) Kind() transport.Kind               { return transport.KindHTTP }
func (observabilityTestTransport) Endpoint() string                   { return "http://example.test" }
func (observabilityTestTransport) Operation() string                  { return "/v1/applications/{application_id}" }
func (tr observabilityTestTransport) RequestHeader() transport.Header { return tr.header }
func (tr observabilityTestTransport) ReplyHeader() transport.Header   { return tr.header }

func TestServerObservability_RedactsPayloadAndCorrelatesTrace(t *testing.T) {
	previousLogger := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(&traceLogHandler{Handler: slog.NewJSONHandler(&output, nil)}))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	meterProvider := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	meter := meterProvider.Meter("test")
	counter, _ := meter.Int64Counter("test.requests")
	duration, _ := meter.Float64Histogram("test.duration")
	propagator := propagation.TraceContext{}
	operations := newObservableOperationRegistry()
	operations.register("http", []string{"/v1/applications/{application_id}"})

	header := observabilityTestHeader{}
	header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx := transport.NewServerContext(context.Background(), observabilityTestTransport{header: header})
	middleware := newServerObservabilityMiddleware(tracerProvider, propagator, counter, duration, operations.normalize)
	_, err := middleware(func(ctx context.Context, request any) (any, error) {
		return nil, kratoserrors.New(409, "VERSION_CONFLICT", "secret=must-not-appear")
	})(ctx, map[string]string{"authorization": "Bearer must-not-appear"})
	if err == nil {
		t.Fatal("middleware error = nil")
	}

	logged := output.String()
	for _, forbidden := range []string{"must-not-appear", "authorization", "secret="} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("log contains sensitive value %q: %s", forbidden, logged)
		}
	}
	for _, required := range []string{"VERSION_CONFLICT", "/v1/applications/{application_id}", "4bf92f3577b34da6a3ce929d0e0e4736"} {
		if !strings.Contains(logged, required) {
			t.Fatalf("log missing %q: %s", required, logged)
		}
	}
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("ended spans = %#v", ended)
	}
}

func TestObservableOperationRegistry_BoundsUnknownInput(t *testing.T) {
	registry := newObservableOperationRegistry()
	registry.register("http", []string{"/v1/applications/{application_id}"})
	if got := registry.normalize("http", "/v1/applications/{application_id}"); got != "/v1/applications/{application_id}" {
		t.Fatalf("known operation = %q", got)
	}
	if got := registry.normalize("http", "/attacker/controlled/value"); got != "http.unmatched" {
		t.Fatalf("unknown operation = %q", got)
	}
}

func TestParseSlogLevel(t *testing.T) {
	for _, value := range []string{"", "DEBUG", "INFO", "WARN", "ERROR"} {
		if _, err := parseSlogLevel(value); err != nil {
			t.Fatalf("parseSlogLevel(%q): %v", value, err)
		}
	}
	if _, err := parseSlogLevel("TRACE"); err == nil {
		t.Fatal("parseSlogLevel(TRACE) error = nil")
	}
}
