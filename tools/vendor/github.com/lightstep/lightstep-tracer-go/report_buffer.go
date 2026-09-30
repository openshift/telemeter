package lightstep

import (
	"time"
)

type reportBuffer struct {
	rawSpans []RawSpan

	// droppedSpanCount is the total number of spans that have been dropped
	// (either because the buffer was full when the span arrived or because a report
	// failed and there wasn't sufficient room to re-enqueue the spans back into the
	// following report).
	//
	// This counter increases continually until is has successfully been reported
	// to the telemetry provider by being attached to a report that doesn't return
	// an error.
	droppedSpanCount int64
	// reportedDroppedSpanCount is a counter that tracks the cumulative value of what
	// has been reported to the service through an event status report. This should
	// always be smaller than droppedSpanCount because droppedSpanCount is monotonically
	// increasing and this only represents the value reported in the past.
	// To determine what to report in the next event status report, subtract the previously
	// reported value from the updated dropped spans count (see reportDroppedSpanCount).
	reportedDroppedSpanCount int64

	// logEncoderErrorCount is the total number of spans that have been rejected
	// because of a log encoder error.
	//
	// This counter increases continually until is has successfully been reported
	// to the telemetry provider by being attached to a report that doesn't return
	// an error.
	logEncoderErrorCount int64
	// reportedLogEncoderErrorCount is a counter that tracks the cumulative value of what
	// has been reported to the service through an event status report. This should
	// always be smaller than logEncoderErrorCount because logEncoderErrorCount is monotonically
	// increasing and this only represents the value reported in the past.
	// To determine what to report in the next event status report, subtract the previously
	// reported value from the updated dropped spans count (see reportLogEncoderErrorCount).
	reportedLogEncoderErrorCount int64

	reportStart time.Time
	reportEnd   time.Time
}

func newSpansBuffer(size int) (b reportBuffer) {
	b.rawSpans = make([]RawSpan, 0, size)
	b.reportStart = time.Time{}
	b.reportEnd = time.Time{}
	return
}

func (b *reportBuffer) isHalfFull() bool {
	return len(b.rawSpans) > cap(b.rawSpans)/2
}

func (b *reportBuffer) setCurrent(now time.Time) {
	b.reportStart = now
	b.reportEnd = now
}

func (b *reportBuffer) setFlushing(now time.Time) {
	b.reportEnd = now
}

func (b *reportBuffer) clear() {
	b.rawSpans = b.rawSpans[:0]
	b.reportStart = time.Time{}
	b.reportEnd = time.Time{}
	b.droppedSpanCount = 0
	b.reportedDroppedSpanCount = 0
	b.logEncoderErrorCount = 0
	b.reportedLogEncoderErrorCount = 0
}

func (b *reportBuffer) addSpan(span RawSpan) {
	if len(b.rawSpans) == cap(b.rawSpans) {
		b.droppedSpanCount++
		return
	}
	b.rawSpans = append(b.rawSpans, span)
}

// mergeFrom combines the spans and metadata in `from` with `into`,
// returning with `from` empty and `into` having a subset of the
// combined data.
func (b *reportBuffer) mergeFrom(from *reportBuffer) {
	b.droppedSpanCount += from.droppedSpanCount
	b.reportedDroppedSpanCount += from.reportedDroppedSpanCount
	b.logEncoderErrorCount += from.logEncoderErrorCount
	b.reportedLogEncoderErrorCount += from.reportedLogEncoderErrorCount

	if from.reportStart.Before(b.reportStart) {
		b.reportStart = from.reportStart
	}
	if from.reportEnd.After(b.reportEnd) {
		b.reportEnd = from.reportEnd
	}

	// Note: Somewhat arbitrarily dropping the spans that won't
	// fit; could be more principled here to avoid bias.
	have := len(b.rawSpans)
	space := cap(b.rawSpans) - have
	unreported := len(from.rawSpans)

	if space > unreported {
		space = unreported
	}

	b.rawSpans = append(b.rawSpans, from.rawSpans[0:space]...)

	if unreported > space {
		b.droppedSpanCount += int64(unreported - space)
	}

	from.clear()
}

func (b *reportBuffer) reportDroppedSpanCount() int64 {
	var toReport int64
	toReport, b.reportedDroppedSpanCount = b.droppedSpanCount-b.reportedDroppedSpanCount, b.droppedSpanCount
	return toReport
}

func (b *reportBuffer) reportLogEncoderErrorCount() int64 {
	var toReport int64
	toReport, b.reportedLogEncoderErrorCount = b.logEncoderErrorCount-b.reportedLogEncoderErrorCount, b.logEncoderErrorCount
	return toReport
}
