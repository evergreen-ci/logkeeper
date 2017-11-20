package logkeeper

import (
	"net/http"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
)

// Logger is a middleware handler that logs the request as it goes in and the response as it goes out.
type Logger struct {
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

	return &Logger{ids}
}

func (l *Logger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	start := time.Now()
	reqID := <-l.ids

	defer func() {
		if err := recover(); err != nil {
			if rw.Header().Get("Content-Type") == "" {
				rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			}

			rw.WriteHeader(http.StatusInternalServerError)

			grip.Critical(message.Fields{
				"stack":    message.NewStack(2, "").Raw(),
				"panic":    err,
				"action":   "aborted",
				"request":  reqID,
				"duration": time.Since(start),
				"path":     r.URL.Path,
				"span":     time.Since(start).String(),
			})
		}
	}()

	next(rw, r)

	res := rw.(negroni.ResponseWriter)

	if res.Status() >= 400 {
		grip.Warning(message.Fields{
			"method":   r.Method,
			"remote":   r.RemoteAddr,
			"request":  reqID,
			"path":     r.URL.Path,
			"duration": time.Since(start),
			"action":   "completed",
			"status":   res.Status(),
			"outcome":  http.StatusText(res.Status()),
			"span":     time.Since(start).String(),
		})
	}
}
