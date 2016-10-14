package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/evergreen-ci/logkeeper"
	"github.com/phyber/negroni-gzip/gzip"
	"github.com/tylerb/graceful"
	"gopkg.in/mgo.v2"
)

// Logger is a middleware handler that logs the request as it goes in and the response as it goes out.
type Logger struct {
	// Logger inherits from log.Logger used to log messages with the Logger middleware
	*log.Logger
	// ids is a channel producing unique, autoincrementing request ids that are included in logs.
	ids chan int
}

// NewLogger returns a new Logger instance
func NewLogger() *Logger {
	ids := make(chan int, 100)
	go func() {
		reqId := 0
		for {
			ids <- reqId
			reqId++
		}
	}()

	return &Logger{log.New(os.Stdout, "[logkeeper] ", log.Lmicroseconds), ids}
}

func (l *Logger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	start := time.Now()
	reqId := <-l.ids
	l.Printf("Started (%v) %s %s %s", reqId, r.Method, r.URL.Path, r.RemoteAddr)

	next(rw, r)

	res := rw.(negroni.ResponseWriter)
	l.Printf("Completed (%v) %v %s in %v", reqId, res.Status(), http.StatusText(res.Status()), time.Since(start))
}

func main() {
	httpPort := flag.Int("port", 8080, "port to listen on for HTTP.")
	dbHost := flag.String("dbhost", "localhost:27017", "host/port to connect to DB server. Comma separated.")
	rsName := flag.String("rsName", "", "name of replica set that the DB instances belong to. "+
		"Leave empty for stand-alone and mongos instances.")
	flag.Parse()

	dialInfo := mgo.DialInfo{
		Addrs: strings.Split(*dbHost, ","),
	}

	if *rsName != "" {
		dialInfo.ReplicaSetName = *rsName
	}

	session, err := mgo.DialWithInfo(&dialInfo)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	lk := logkeeper.New(session, logkeeper.Options{
		DB:  "buildlogs",
		URL: fmt.Sprintf("http://localhost:%v", *httpPort),
	})
	router := lk.NewRouter()
	n := negroni.New()
	n.Use(NewLogger())
	n.Use(negroni.NewRecovery())                 // part of negroni Classic settings
	n.Use(negroni.NewStatic(http.Dir("public"))) // part of negroni Classic settings
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(router)

	graceful.Run(fmt.Sprintf(":%v", *httpPort), 10*time.Second, n)
}
