package logkeeper

import (
	"context"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"net/http"
	"time"
)

const (
	exportInterval = 15 * time.Second
	exportTimeout  = exportInterval * 2
	packageName    = "github.com/evergreen-ci/logkeeper"
)

func (lk *logkeeper) initOtel(ctx context.Context) error {
	if lk.opts.TraceCollectorEndpoint == "" {
		traceExporter, err := stdouttrace.New()
		if err != nil {
			return err
		}
		lk.tracer = sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.TraceIDRatioBased(lk.opts.TraceSampleRatio)),
			sdktrace.WithBatcher(traceExporter, sdktrace.WithBatchTimeout(time.Second)),
		).Tracer(packageName)
		return nil
	}

	r, err := serviceResource(ctx)
	if err != nil {
		return errors.Wrap(err, "making host resource")
	}

	lk.otelGrpcConn, err = grpc.DialContext(ctx,
		lk.opts.TraceCollectorEndpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(nil)),
	)
	if err != nil {
		return errors.Wrapf(err, "opening gRPC connection to '%s'", lk.opts.TraceCollectorEndpoint)
	}

	client := otlptracegrpc.NewClient(otlptracegrpc.WithGRPCConn(lk.otelGrpcConn))
	traceExporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return errors.Wrap(err, "initializing otel exporter")
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(lk.opts.TraceSampleRatio)),
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(r),
	)
	tp.RegisterSpanProcessor(utility.NewAttributeSpanProcessor())
	otel.SetTracerProvider(tp)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		grip.Error(errors.Wrap(err, "otel error"))
	}))

	lk.tracer = tp.Tracer(packageName)

	lk.closers = append(lk.closers, closerOp{
		name: "tracer provider shutdown",
		closerFn: func(ctx context.Context) error {
			catcher := grip.NewBasicCatcher()
			catcher.Wrap(tp.Shutdown(ctx), "trace provider shutdown")
			catcher.Wrap(traceExporter.Shutdown(ctx), "trace exporter shutdown")
			catcher.Wrap(lk.otelGrpcConn.Close(), "closing gRPC connection")

			return catcher.Resolve()
		},
	})

	return nil
}

func serviceResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("logkeeper")),
	)
}

func handleFunc(pattern string, handlerFunc func(http.ResponseWriter, *http.Request)) {
	// Configure the "http.route" for the HTTP instrumentation.
	handler := otelhttp.WithRouteTag(pattern, http.HandlerFunc(handlerFunc))
	mux.Handle(pattern, handler)
}
