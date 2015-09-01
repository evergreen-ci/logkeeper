package main

import (
	"flag"
	"fmt"
	"github.com/codegangsta/negroni"
	"github.com/evergreen-ci/logkeeper"
	"github.com/phyber/negroni-gzip/gzip"
	"github.com/tylerb/graceful"
	"gopkg.in/mgo.v2"
	"time"
	"log"
	"net/http"
	"os"
)

type Logger struct {
	*log.Logger
}

// Custom logger to label logs with [logkeeper] and timestamp
func NewLogger() *Logger {
	return &Logger{log.New(os.Stdout, "[logkeeper] ", 0)}
}

// Identical to negroni logger, but with timestamps added
func (l *Logger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	start := time.Now()
	l.Printf(time.Now().Format(time.RFC3339) + " Started %s %s", r.Method, r.URL.Path)

	next(rw, r)

	res := rw.(negroni.ResponseWriter)
	l.Printf(time.Now().Format(time.RFC3339) + " Completed %v %s in %v", res.Status(), http.StatusText(res.Status()), time.Since(start))
}

func main() {
	var httpPort = flag.Int("port", 8080, "port to listen on for HTTP")
	var dbHost = flag.String("dbhost", "localhost:27017", "host/port to connect to DB server")
	flag.Parse()

	session, err := mgo.Dial(*dbHost)
	if err != nil {
		fmt.Println(err)
		return
	}

	lk := logkeeper.New(session, logkeeper.Options{
		DB:  "buildlogs",
		URL: fmt.Sprintf("http://localhost:%v", *httpPort),
	})
	router := lk.NewRouter()
	n := negroni.New()
	n.Use(NewLogger())
	n.Use(negroni.NewRecovery()) // part of negroni Classic settings
	n.Use(negroni.NewStatic(http.Dir("public"))) // part of negroni Classic settings
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(router)

	graceful.Run(fmt.Sprintf(":%v", *httpPort), 10*time.Second, n)
}
