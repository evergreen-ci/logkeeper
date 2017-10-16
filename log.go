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

	next(rw, r)

	res := rw.(negroni.ResponseWriter)

	if res.Status() > 299 {
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
