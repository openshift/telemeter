package tracing

import (
	"context"
	"fmt"
	"net"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	propjaeger "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/trace/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv"
	"go.opentelemetry.io/otel/trace"
)

// Adjusted from github.com/observatorium/api/tracing/tracing.go

// EndpointType represents the type of the tracing endpoint.
type EndpointType string

const (
	EndpointTypeCollector EndpointType = "collector"
	EndpointTypeAgent     EndpointType = "agent"
)

// InitTracer creates an OTel TracerProvider that exports the traces to a Jaeger agent/collector.
func InitTracer(
	ctx context.Context,
	serviceName string,
	endpoint string,
	endpointTypeRaw string,
	samplingFraction float64,
) (tp trace.TracerProvider, err error) {
	tp = trace.NewNoopTracerProvider()

	if endpoint == "" {
		return tp, nil
	}

	var endpointOption jaeger.EndpointOption
	switch EndpointType(endpointTypeRaw) {
	case EndpointTypeAgent:
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return tp, fmt.Errorf("cannot parse tracing endpoint host and port: %w", err)
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
		return tp, fmt.Errorf("unknown tracing endpoint type provided")
	}

	exp, err := jaeger.NewRawExporter(
		endpointOption,
	)
	if err != nil {
		return tp, fmt.Errorf("create jaeger exporter: %w", err)
	}

	r, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)))
	if err != nil {
		return tp, fmt.Errorf("create resource: %w", err)
	}

	tp = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(r),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(samplingFraction)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propjaeger.Jaeger{},
		propagation.Baggage{},
	))

	return tp, nil
}

type OtelErrorHandler struct {
	Logger log.Logger
}

func (oh OtelErrorHandler) Handle(err error) {
	level.Error(oh.Logger).Log("msg", "opentelemetry", "err", err.Error())
}
