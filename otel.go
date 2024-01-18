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
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	otelTrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	packageName = "github.com/evergreen-ci/logkeeper"
)

type Otel struct {
	Tracer       *otelTrace.Tracer
	OtelGrpcConn *grpc.ClientConn
	Closers      []closerOp
}

func initOtel(ctx context.Context, name string, collectorEndpoint string) (Otel, error) {
	var (
		o            Otel
		otelGrpcConn *grpc.ClientConn
		tracer       = otel.GetTracerProvider().Tracer("noop_tracer") // default noop
		closers      []closerOp
	)
	if collectorEndpoint == "" {
		// defaults to NoopTracerProvider
		tracer = otel.GetTracerProvider().Tracer(packageName)
		o.Tracer = &tracer
		return o, nil
	}

	r, err := serviceResource(ctx, name)
	if err != nil {
		o.Tracer = &tracer
		return o, errors.Wrap(err, "making host resource")
	}

	otelGrpcConn, err = grpc.DialContext(ctx,
		collectorEndpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(nil)),
	)
	if err != nil {
		o.Tracer = &tracer
		return o, errors.Wrapf(err, "opening gRPC connection to '%s'", collectorEndpoint)
	}

	client := otlptracegrpc.NewClient()
	traceExporter, err := otlptrace.New(ctx, client)
	if err != nil {
		o.Tracer = &tracer
		return o, errors.Wrap(err, "initializing otel exporter")
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(r),
	)
	tp.RegisterSpanProcessor(utility.NewAttributeSpanProcessor())
	otel.SetTracerProvider(tp)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		grip.Error(errors.Wrap(err, "otel error"))
	}))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	tracer = tp.Tracer(packageName)

	closers = append(closers, closerOp{
		name: "tracer provider shutdown",
		closerFn: func(ctx context.Context) error {
			catcher := grip.NewBasicCatcher()
			catcher.Wrap(tp.Shutdown(ctx), "trace provider shutdown")
			catcher.Wrap(traceExporter.Shutdown(ctx), "trace exporter shutdown")
			catcher.Wrap(otelGrpcConn.Close(), "closing gRPC connection")

			return catcher.Resolve()
		},
	})

	o.Tracer = &tracer
	o.OtelGrpcConn = otelGrpcConn
	o.Closers = closers
	return o, nil
}

func closeOtel(ctx context.Context, closers []closerOp) {
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
		"message": "calling closers",
	}))
}

func serviceResource(ctx context.Context, name string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(name)),
	)
}
