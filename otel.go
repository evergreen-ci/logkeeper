package logkeeper

import (
	"context"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

var closers []closerOp

func LoadTraceProvider(ctx context.Context, collectorEndpoint string, sampleRatio float64) error {
	if collectorEndpoint == "" {
		return nil
	}
	r, err := serviceResource(ctx)
	if err != nil {
		return errors.Wrap(err, "making otel host resource")
	}
	client := otlptracegrpc.NewClient(otlptracegrpc.WithEndpoint(collectorEndpoint))

	traceExporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return errors.Wrap(err, "initializing otel exporter")
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithSampler(
			sdktrace.ParentBased(
				// default if no parent span received
				sdktrace.TraceIDRatioBased(sampleRatio),
				// if parent span received, always sample
				sdktrace.WithRemoteParentNotSampled(sdktrace.AlwaysSample()),
				sdktrace.WithLocalParentSampled(sdktrace.AlwaysSample()))),
		sdktrace.WithResource(r),
	)
	tp.RegisterSpanProcessor(utility.NewAttributeSpanProcessor())
	otel.SetTracerProvider(tp)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		grip.Error(errors.Wrap(err, "otel error"))
	}))

	closers = append(closers, closerOp{
		name: "tracer provider shutdown",
		closerFn: func(ctx context.Context) error {
			catcher := grip.NewBasicCatcher()
			catcher.Wrap(tp.Shutdown(ctx), "trace provider shutdown")
			catcher.Wrap(traceExporter.Shutdown(ctx), "trace exporter shutdown")
			return catcher.Resolve()
		},
	})
	return nil
}

func Close(ctx context.Context) {
	catcher := grip.NewBasicCatcher()
	for idx, closer := range closers {
		if closer.closerFn == nil {
			continue
		}

		grip.Info(message.Fields{
			"message": "calling closer",
			"index":   idx,
			"closer":  closer.name,
		})

		catcher.Add(closer.closerFn(ctx))
	}

	grip.Error(message.WrapError(catcher.Resolve(), message.Fields{
		"message": "calling logkeeper closers",
	}))
}

func serviceResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("logkeeper")),
	)
}
