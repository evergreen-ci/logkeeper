package exporter

import (
	"context"
	"github.com/mongodb/grip"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"sync"
	"time"
)

var (
	zeroTime time.Time
	_        trace.SpanExporter = &Exporter{}
)

type Exporter struct {
	trace.SpanExporter

	loggerMu   sync.Mutex
	stoppedMu  sync.RWMutex
	timestamps bool
	stopped    bool
}

// New creates an Exporter with the passed options.
func New() *Exporter {
	return &Exporter{}
}

func (e *Exporter) ExportSpans(_ context.Context, spans []trace.ReadOnlySpan) error {
	e.stoppedMu.RLock()
	stopped := e.stopped
	e.stoppedMu.RUnlock()
	if stopped {
		return nil
	}

	if len(spans) == 0 {
		return nil
	}

	stubs := tracetest.SpanStubsFromReadOnlySpans(spans)

	e.loggerMu.Lock()
	defer e.loggerMu.Unlock()
	for i := range stubs {
		stub := &stubs[i]
		// Remove timestamps
		if !e.timestamps {
			stub.StartTime = zeroTime
			stub.EndTime = zeroTime
			for j := range stub.Events {
				ev := &stub.Events[j]
				ev.Time = zeroTime
			}
		}

		grip.Info(stubs)
	}
	return nil
}

func (e *Exporter) Shutdown(ctx context.Context) error {
	e.stoppedMu.Lock()
	e.stopped = true
	e.stoppedMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
