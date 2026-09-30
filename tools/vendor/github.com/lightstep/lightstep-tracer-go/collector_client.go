package lightstep

import (
	"context"
	"io"
	"net/http"

	"github.com/lightstep/lightstep-tracer-common/golang/gogo/collectorpb"
)

var accessTokenHeader = http.CanonicalHeaderKey("Lightstep-Access-Token")

// Connection describes a closable connection. Exposed for testing.
type Connection interface {
	io.Closer
}

// ConnectorFactory is for testing purposes.
type ConnectorFactory func() (interface{}, Connection, error)

// collectorResponse encapsulates internal grpc/http responses.
type collectorResponse interface {
	GetErrors() []string
	Disable() bool
	DevMode() bool
}

// Collector encapsulates custom transport of protobuf messages
type Collector interface {
	Report(context.Context, *collectorpb.ReportRequest) (*collectorpb.ReportResponse, error)
}

type reportRequest struct {
	protoRequest *collectorpb.ReportRequest
	httpRequest  *http.Request
}

// SplitByParts splits reportRequest into given number of parts.
// Beware, that parts=0 panics.
func (rr reportRequest) SplitByParts(parts int) []reportRequest {

	if rr.protoRequest == nil || len(rr.protoRequest.Spans) == 0 || parts <= 1 {
		return []reportRequest{rr}
	}
	spans := rr.protoRequest.Spans

	maxSize := len(rr.protoRequest.Spans) / parts
	if len(rr.protoRequest.Spans)%parts > 0 {
		maxSize++
	}

	var rrs []reportRequest
	for len(spans) > 0 {
		s := maxSize
		if len(spans) < s {
			s = len(spans)
		}

		r := rr
		r.protoRequest.Spans = make([]*collectorpb.Span, s)
		copy(r.protoRequest.Spans, spans[:s])
		spans = spans[s:]
		rrs = append(rrs, r)
	}

	return rrs
}

// collectorClient encapsulates internal grpc/http transports.
type collectorClient interface {
	Report(context.Context, reportRequest) (collectorResponse, error)
	Translate(*collectorpb.ReportRequest) (reportRequest, error)
	ConnectClient() (Connection, error)
	ShouldReconnect() bool
}

func newCollectorClient(opts Options) (collectorClient, error) {
	if opts.CustomCollector != nil {
		return newCustomCollector(opts), nil
	}
	if opts.UseHttp {
		return newHTTPCollectorClient(opts)
	}
	if opts.UseGRPC {
		return newGrpcCollectorClient(opts)
	}

	// No transport specified, defaulting to HTTP
	return newHTTPCollectorClient(opts)
}
