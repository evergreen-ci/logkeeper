package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/evergreen-ci/logkeeper"
	gorillaCtx "github.com/gorilla/context"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	"github.com/urfave/negroni"
	"gopkg.in/mgo.v2"
)

func main() {
	defer recovery.LogStackTraceAndExit("logkeeper.main")

	httpPort := flag.Int("port", 8080, "port to listen on for HTTP.")
	dbHost := flag.String("dbhost", "localhost:27017", "host/port to connect to DB server. Comma separated.")
	rsName := flag.String("rsName", "", "name of replica set that the DB instances belong to. "+
		"Leave empty for stand-alone and mongos instances.")
	logPath := flag.String("logpath", "logkeeperapp.log", "path to log file")
	maxRequestSize := flag.Int("maxRequestSize", 1024*1024*32,
		"maximum size for a request in bytes, defaults to 32 MB (in bytes)")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sender, err := logkeeper.GetSender(*logPath)
	grip.CatchEmergencyFatal(err)
	defer sender.Close()

	grip.CatchEmergencyFatal(grip.SetSender(sender))

	dialInfo := mgo.DialInfo{
		Addrs: strings.Split(*dbHost, ","),
	}

	if *rsName != "" {
		dialInfo.ReplicaSetName = *rsName
	}

	session, err := mgo.DialWithInfo(&dialInfo)
	grip.CatchEmergencyFatal(err)

	lk := logkeeper.New(session, logkeeper.Options{
		DB:             "buildlogs",
		URL:            fmt.Sprintf("http://localhost:%v", *httpPort),
		MaxRequestSize: *maxRequestSize,
	})

	logkeeper.StartBackgroundLogging(ctx)

	catcher := grip.NewCatcher()
	router := lk.NewRouter()
	n := negroni.New()
	n.Use(logkeeper.NewLogger())                 // includes recovery and logging
	n.Use(negroni.NewStatic(http.Dir("public"))) // part of negroni Classic settings
	n.UseHandler(gorillaCtx.ClearHandler(router))

	serviceWait := &sync.WaitGroup{}
	lkService := getService(fmt.Sprintf(":%v", *httpPort), n)
	serviceWait.Add(1)
	go func() {
		defer recovery.LogStackTraceAndContinue("logkeeper service")
		defer serviceWait.Done()
		catcher.Add(listenServeAndHandleErrs(lkService))
	}()

	pprofService := getService("127.0.0.1:2285", logkeeper.GetHandlerPprof())
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

	grip.CatchEmergencyFatal(catcher.Resolve())
}

func listenServeAndHandleErrs(s *http.Server) error {
	if s == nil {
		return errors.New("no server defined")
	}
	err := s.ListenAndServe()
	if err == http.ErrServerClosed {
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
