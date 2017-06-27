package logkeeper

import (
	"net/http"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/mongodb/grip"
)

// Logger is a middleware handler that logs the request as it goes in and the response as it goes out.
type Logger struct {
	// Logger inherits from log.Logger used to log messages with the Logger middleware
	grip grip.Journaler
	// ids is a channel producing unique, autoincrementing request ids that are included in logs.
	ids chan int
}

func (l *Logger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	start := time.Now()
	reqId := <-l.ids
	SetCtxRequestId(reqId, r)
	l.grip.Infof("Started (%v) %s %s %s", reqId, r.Method, r.URL.Path, r.RemoteAddr)

	next(rw, r)

	res := rw.(negroni.ResponseWriter)

	l.grip.Infof("Completed (%v) %v %s in %v", reqId, res.Status(), http.StatusText(res.Status()), time.Since(start))
}

// NewLogger returns a new Logger instance
func (lk *logKeeper) NewLogger() *Logger {
	ids := make(chan int, 100)
	go func() {
		reqId := 0
		for {
			ids <- reqId
			reqId++
		}
	}()

	logger := grip.NewJournaler("logkeeper")
	grip.Error(logger.SetSender(grip.GetSender()))

	return &Logger{
		grip: logger,
		ids:  ids,
	}
}
