package logkeeper

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/sometimes"
	"github.com/pkg/errors"
	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/stat"
)

const (
	remoteAddrHeaderName = "X-Cluster-Client-Ip"
	chanBufferSize       = 1000
	loggerStatsInterval  = 10 * time.Second
	durationsLimit       = 100000
	logErrorPercentage   = 10
)

// Logger is a middleware handler that aggregates statistics on response durations.
// If a handler panics Logger will recover the panic and log its error.
type Logger struct {
	// ids is a channel producing unique, auto-incrementing request ids.
	// A request's id can be extracted from its context with GetCtxRequestId.
	ids chan int

	newDurations     chan routeDuration
	durationsByRoute map[string][]float64
	cacheIsFull      bool
	lastReset        time.Time
}

type routeDuration struct {
	route    string
	duration time.Duration
}

// NewLogger returns a new Logger instance
func NewLogger(ctx context.Context) *Logger {
	l := &Logger{
		ids:              make(chan int, chanBufferSize),
		newDurations:     make(chan routeDuration, chanBufferSize),
		durationsByRoute: make(map[string][]float64),
		lastReset:        time.Now(),
	}

	go l.incrementIDLoop(ctx)
	go l.durationLoggerLoop(ctx)

	return l
}

func (l *Logger) durationLoggerLoop(ctx context.Context) {
	ticker := time.NewTicker(loggerStatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.flushStats()
		case duration := <-l.newDurations:
			l.recordDuration(duration)

			if l.cacheIsFull {
				l.flushStats()
				ticker.Reset(loggerStatsInterval)
			}
		}
	}
}

func (l *Logger) incrementIDLoop(ctx context.Context) {
	reqId := 0

	for {
		select {
		case <-ctx.Done():
			return
		case l.ids <- reqId:
			reqId++
		}
	}
}

func (l *Logger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	start := time.Now()
	reqID := <-l.ids
	r = SetCtxRequestId(reqID, r)

	remote := r.Header.Get(remoteAddrHeaderName)
	if remote == "" {
		remote = r.RemoteAddr
	}

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
				"span":     time.Since(start).String(),
				"remote":   remote,
				"path":     r.URL.Path,
			})
		}
	}()

	next(rw, r)

	grip.ErrorWhen(
		sometimes.Percent(logErrorPercentage),
		message.WrapError(
			l.addToCache(r, time.Since(start)),
			message.Fields{
				"message": "adding duration to buffer",
				"path":    r.URL.Path,
			},
		),
	)
}

func (l *Logger) addToCache(r *http.Request, duration time.Duration) error {
	route := mux.CurrentRoute(r)
	path, err := route.GetPathTemplate()
	if err != nil {
		return errors.Wrap(err, "getting path template")
	}

	select {
	case l.newDurations <- routeDuration{route: path, duration: duration}:
		return nil
	default:
		return errors.New("durations buffer is full")
	}
}

func (l *Logger) recordDuration(duration routeDuration) {
	l.durationsByRoute[duration.route] = append(l.durationsByRoute[duration.route], float64(duration.duration.Milliseconds()))

	if len(l.durationsByRoute[duration.route]) == durationsLimit {
		l.cacheIsFull = true
	}
}

func (l *Logger) flushStats() {
	for route, durations := range l.durationsByRoute {
		msg := message.Fields{
			"message":  "route duration stats",
			"route":    route,
			"count":    len(durations),
			"interval": time.Since(l.lastReset),
		}
		if len(durations) > 0 {
			msg["sum_ms"] = floats.Sum(durations)
			msg["max_ms"] = floats.Max(durations)
			msg["min_ms"] = floats.Min(durations)
			msg["mean_ms"] = stat.Mean(durations, nil)
			msg["std_dev"] = stat.StdDev(durations, nil)
		}
		grip.Info(msg)
	}

	l.resetStats()
}

func (l *Logger) resetStats() {
	for route := range l.durationsByRoute {
		l.durationsByRoute[route] = l.durationsByRoute[route][:0]
	}
	l.lastReset = time.Now()
	l.cacheIsFull = false
}
