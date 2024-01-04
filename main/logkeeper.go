package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/mongodb/grip/send"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/storage"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"github.com/urfave/negroni"
)

func main() {
	defer recovery.LogStackTraceAndExit("logkeeper.main")

	httpPort := flag.Int("port", 8080, "port to listen on for HTTP.")
	localPath := flag.String("localPath", "", "local path to save data to. Omit to save data to S3.")
	logPath := flag.String("logpath", "logkeeperapp.log", "path to log file")
	maxRequestSize := flag.Int("maxRequestSize", 1024*1024*32,
		"maximum size for a request in bytes, defaults to 32 MB (in bytes)")
	traceCollectorEndpoint := flag.String("traceCollectorEndpoint", "", "OTEL Collector URL")
	_ = flag.String("dbhost", "", "LEGACY: this option is ignored")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sender, err := logkeeper.GetSender(ctx, *logPath)
	grip.EmergencyFatal(err)
	defer func(sender send.Sender) {
		grip.Error(message.WrapError(sender.Close(), "failed to close sender"))
	}(sender)
	grip.EmergencyFatal(grip.SetSender(sender))

	bucket, err := makeBucket(localPath)
	grip.EmergencyFatal(errors.Wrap(err, "getting bucket"))
	grip.EmergencyFatal(errors.Wrap(env.SetBucket(&bucket), "setting bucket in env"))

	lk := logkeeper.NewLogkeeper(
		ctx,
		logkeeper.LogkeeperOptions{
			URL:                    fmt.Sprintf("http://localhost:%v", *httpPort),
			MaxRequestSize:         *maxRequestSize,
			TraceCollectorEndpoint: *traceCollectorEndpoint,
		},
	)
	defer lk.Close(ctx)
	go logkeeper.BackgroundLogging(ctx)

	catcher := grip.NewBasicCatcher()
	router := lk.NewRouter()
	router.Use(logkeeper.NewLogger(ctx).Middleware)
	n := negroni.New()
	n.Use(negroni.NewStatic(http.Dir("public"))) // part of negroni Classic settings
	n.UseHandler(router)

	serviceWait := &sync.WaitGroup{}
	lkService := getService(fmt.Sprintf(":%v", *httpPort), n)
	serviceWait.Add(1)
	go func() {
		defer recovery.LogStackTraceAndContinue("logkeeper service")
		defer serviceWait.Done()
		catcher.Add(listenServeAndHandleErrs(lkService))
	}()

	pprofService := getService("127.0.0.1:2285", logkeeper.GetHandlerPprof(ctx))
	serviceWait.Add(1)
	go func() {
		defer recovery.LogStackTraceAndContinue("pprof service")
		defer serviceWait.Done()
		catcher.Add(listenServeAndHandleErrs(pprofService))
	}()

	gracefulWait := &sync.WaitGroup{}
	gracefulWait.Add(1)
	go gracefulShutdownForSIGTERM(ctx, []*http.Server{lkService, pprofService}, gracefulWait, catcher)

	serviceWait.Wait()

	grip.Notice("waiting for web services to terminate gracefully")
	gracefulWait.Wait()

	grip.EmergencyFatal(catcher.Resolve())
}

func listenServeAndHandleErrs(s *http.Server) error {
	if s == nil {
		return errors.New("no server defined")
	}
	err := s.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		grip.Noticef("server '%s' closed, no longer serving HTTP requests", s.Addr)
		return nil
	}
	return err
}

func getService(addr string, n http.Handler) *http.Server {
	grip.Info(message.Fields{
		"message":  "starting service",
		"revision": logkeeper.BuildRevision,
		"addr":     addr,
	})

	return &http.Server{
		Addr:              addr,
		Handler:           n,
		ReadTimeout:       time.Minute,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      time.Minute,
	}

}

func gracefulShutdownForSIGTERM(ctx context.Context, servers []*http.Server, gracefulWait *sync.WaitGroup, catcher grip.Catcher) {
	defer recovery.LogStackTraceAndContinue("graceful shutdown")
	defer gracefulWait.Done()
	sigChan := make(chan os.Signal, len(servers))
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	grip.Info("received SIGTERM, terminating web service")
	wg := sync.WaitGroup{}
	for _, s := range servers {
		if s == nil {
			continue
		}
		wg.Add(1)
		go func(server *http.Server) {
			defer recovery.LogStackTraceAndContinue("server shutdown")
			defer wg.Done()
			catcher.Add(server.Shutdown(ctx))
		}(s)
	}
	wg.Wait()
}

func makeBucket(localPath *string) (storage.Bucket, error) {
	if *localPath != "" {
		return storage.NewBucket(storage.BucketOpts{
			Location: storage.PailLocal,
			Path:     *localPath,
		})
	}

	return storage.NewBucket(storage.BucketOpts{Location: storage.PailS3})
}
