package tracing

import (
	"context"
	"fmt"
	"net"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	propjaeger "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

// Adjusted from github.com/observatorium/api/tracing/tracing.go

// EndpointType represents the type of the tracing endpoint.
type EndpointType string

const (
	EndpointTypeCollector EndpointType = "collector"
	EndpointTypeAgent     EndpointType = "agent"
	EndpointTypeOTel      EndpointType = "otel"
)

// InitTracer creates an OTel TracerProvider that exports the traces to a Jaeger agent/collector.
func InitTracer(
	ctx context.Context,
	serviceName string,
	endpoint string,
	endpointTypeRaw string,
	samplingFraction float64,
) (trace.TracerProvider, error) {
	nopTracerProvider := trace.NewNoopTracerProvider()
	otel.SetTracerProvider(nopTracerProvider)

	if endpoint == "" {
		return nopTracerProvider, nil
	}

	r, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)))
	if err != nil {
		return nopTracerProvider, fmt.Errorf("create resource: %w", err)
	}

	var exporter sdktrace.SpanExporter
	endpointType := EndpointType(endpointTypeRaw)

	switch endpointType {
	case EndpointTypeAgent, EndpointTypeCollector:
		exporter, err = setUpJaegerExporter(endpointType, endpoint)
		if err != nil {
			return nopTracerProvider, fmt.Errorf("setup jaeger exporter: %w", err)
		}
	case EndpointTypeOTel:
		exporter, err = setUpOtelExporter(endpoint)
		if err != nil {
			return nopTracerProvider, fmt.Errorf("setup otel exporter: %w", err)
		}
	default:
		return nopTracerProvider, fmt.Errorf("invalid endpoint type: %s", endpointTypeRaw)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(r),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(samplingFraction)),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propjaeger.Jaeger{},
		propagation.Baggage{},
	))

	return provider, nil
}

func setUpJaegerExporter(endpointType EndpointType, endpoint string) (*jaeger.Exporter, error) {
	var endpointOption jaeger.EndpointOption
	switch endpointType {
	case EndpointTypeAgent:
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("cannot parse tracing endpoint host and port: %w", err)
		}
		endpointOption = jaeger.WithAgentEndpoint(
			jaeger.WithAgentHost(host),
			jaeger.WithAgentPort(port),
		)
	case EndpointTypeCollector:
		endpointOption = jaeger.WithCollectorEndpoint(
			jaeger.WithEndpoint(endpoint),
		)
	default:
		return nil, fmt.Errorf("unknown tracing endpoint type provided")
	}

	exp, err := jaeger.New(
		endpointOption,
	)
	if err != nil {
		return nil, fmt.Errorf("create jaeger exporter: %w", err)
	}
	return exp, nil
}

func setUpOtelExporter(endoint string) (*otlptrace.Exporter, error) {
	exp, err := otlptracehttp.New(context.TODO(), otlptracehttp.WithEndpoint(endoint), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("create otel exporter: %w", err)
	}
	return exp, nil
}

type OtelErrorHandler struct {
	Logger log.Logger
}

func (oh OtelErrorHandler) Handle(err error) {
	level.Error(oh.Logger).Log("msg", "opentelemetry", "err", err.Error())
}
