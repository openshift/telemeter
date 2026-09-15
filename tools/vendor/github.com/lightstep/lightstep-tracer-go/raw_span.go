package lightstep

import (
	"time"
	"unsafe"

	"github.com/opentracing/opentracing-go"
)

// RawSpan encapsulates all state associated with a (finished) LightStep Span.
type RawSpan struct {
	// Those recording the RawSpan should also record the contents of its
	// SpanContext.
	Context SpanContext

	// The SpanID of this SpanContext's first intra-trace reference (i.e.,
	// "parent"), or 0 if there is no parent.
	ParentSpanID uint64

	// The name of the "operation" this span is an instance of. (Called a "span
	// name" in some implementations)
	Operation string

	// We store <start, duration> rather than <start, end> so that only
	// one of the timestamps has global clock uncertainty issues.
	Start    time.Time
	Duration time.Duration

	// Essentially an extension mechanism. Can be used for many purposes,
	// not to be enumerated here.
	Tags opentracing.Tags

	// The span's "microlog".
	Logs []opentracing.LogRecord
}

func (r *RawSpan) Len() int {
	size := r.Context.Len() +
		3*8 +
		len(r.Operation) +
		int(unsafe.Sizeof(r.Start)) +
		int(unsafe.Sizeof(r.Duration))

	for k, v := range r.Tags {
		size += len(k)
		if s, ok := v.(string); ok {
			size += len(s)
		}
	}
	for _, lr := range r.Logs {
		size += int(unsafe.Sizeof(lr.Timestamp))
		for _, f := range lr.Fields {
			size += int(unsafe.Sizeof(f))
		}
	}
	return size

}

// SpanContext holds lightstep-specific Span metadata.
type SpanContext struct {
	// A probabilistically unique identifier for a [multi-span] trace.
	TraceID uint64

	// Most significant bits of a 128-bit TraceID
	TraceIDUpper uint64

	// A probabilistically unique identifier for a span.
	SpanID uint64

	// Propagates sampling decision
	Sampled string

	// The span's associated baggage.
	Baggage map[string]string // initialized on first use
}

// Len returns size in bytes
func (c SpanContext) Len() int {
	size := (3 * 8) + len(c.Sampled)
	for k, v := range c.Baggage {
		size += len(k) + len(v)
	}
	return size
}

// ForeachBaggageItem belongs to the opentracing.SpanContext interface
func (c SpanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	for k, v := range c.Baggage {
		if !handler(k, v) {
			break
		}
	}
}

// WithBaggageItem returns an entirely new basictracer SpanContext with the
// given key:value baggage pair set.
func (c SpanContext) WithBaggageItem(key, val string) SpanContext {
	var newBaggage map[string]string
	if c.Baggage == nil {
		newBaggage = map[string]string{key: val}
	} else {
		newBaggage = make(map[string]string, len(c.Baggage)+1)
		for k, v := range c.Baggage {
			newBaggage[k] = v
		}
		newBaggage[key] = val
	}
	// Use positional parameters so the compiler will help catch new fields.

	return SpanContext{c.TraceID, c.TraceIDUpper, c.SpanID, c.Sampled, newBaggage}
}
