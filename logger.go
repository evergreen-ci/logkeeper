package logkeeper

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/sometimes"
	"github.com/pkg/errors"
	"github.com/urfave/negroni"
	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/stat"
)

const (
	remoteAddrHeaderName = "X-Cluster-Client-Ip"
	chanBufferSize       = 1000
	loggerStatsInterval  = 10 * time.Second
	statsLimit           = 100000
	logErrorPercentage   = 10
	bytesPerMB           = 1000000
)

var (
	durationBins = []float64{0, 250, 500, 1000, 5000, 30000, 60000, 300000, math.MaxFloat64}
	sizeBins     = []float64{0, 0.5, 1, 5, 10, 50, math.MaxFloat64}
)

// Logger is a middleware handler that aggregates statistics on response durations.
// If a handler panics Logger will recover the panic and log its error.
type Logger struct {
	// ids is a channel producing unique, auto-incrementing request ids.
	// A request's id can be extracted from its context with GetCtxRequestId.
	ids chan int

	newDurations chan routeResponse
	statsByRoute map[string]routeStats

	cacheIsFull bool
	lastReset   time.Time
}

type routeStats struct {
	durationMS   []float64
	requestMB    []float64
	responseMB   []float64
	statusCounts map[int]int
}

type routeResponse struct {
	route        string
	duration     time.Duration
	responseSize int
	requestSize  int
	status       int
}

// NewLogger returns a new Logger instance
func NewLogger(ctx context.Context) *Logger {
	l := &Logger{
		ids:          make(chan int, chanBufferSize),
		newDurations: make(chan routeResponse, chanBufferSize),
		statsByRoute: make(map[string]routeStats),
		lastReset:    time.Now(),
	}

	go l.incrementIDLoop(ctx)
	go l.durationLoggerLoop(ctx)

	return l
}

func (l *Logger) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	start := time.Now()
	reqID := <-l.ids
	r = setCtxRequestId(reqID, r)
	r = setStartAtTime(r, start)

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
			l.addToCache(rw, r),
			message.Fields{
				"message": "adding duration to buffer",
				"path":    r.URL.Path,
			},
		),
	)
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

func (l *Logger) addToCache(rw http.ResponseWriter, r *http.Request) error {
	route := mux.CurrentRoute(r)
	path, err := route.GetPathTemplate()
	if err != nil {
		return errors.Wrap(err, "getting path template")
	}

	writer, ok := rw.(negroni.ResponseWriter)
	if !ok {
		return errors.Errorf("response writer has unexpected type '%T'", rw)
	}

	select {
	case l.newDurations <- routeResponse{
		route:        path,
		duration:     time.Since(getRequestStartAt(r.Context())),
		status:       writer.Status(),
		responseSize: writer.Size(),
		requestSize:  int(r.ContentLength),
	}:
		return nil
	default:
		return errors.New("durations buffer is full")
	}
}

func (l *Logger) recordDuration(response routeResponse) {
	stats := l.statsByRoute[response.route]

	stats.durationMS = append(stats.durationMS, float64(response.duration.Milliseconds()))
	stats.requestMB = append(stats.requestMB, float64(response.requestSize)/bytesPerMB)
	stats.responseMB = append(stats.responseMB, float64(response.responseSize)/bytesPerMB)

	if stats.statusCounts == nil {
		stats.statusCounts = make(map[int]int)
	}
	stats.statusCounts[response.status]++

	l.statsByRoute[response.route] = stats

	if len(stats.durationMS) == statsLimit {
		l.cacheIsFull = true
	}
}

func (l *Logger) flushStats() {
	for route, stats := range l.statsByRoute {
		msg := stats.makeMessage()
		msg["route"] = route
		msg["interval"] = time.Since(l.lastReset)

		grip.Info(msg)
	}

	l.resetStats()
}

func (s *routeStats) makeMessage() message.Fields {
	msg := message.Fields{
		"message": "route stats",
		"count":   len(s.durationMS),
	}

	if len(s.durationMS) > 0 {
		msg["service_time_ms"] = sliceStats(s.durationMS, durationBins)
	}
	if len(s.responseMB) > 0 {
		msg["response_sizes_mb"] = sliceStats(s.responseMB, sizeBins)
	}
	if len(s.requestMB) > 0 {
		msg["request_sizes_mb"] = sliceStats(s.requestMB, sizeBins)
	}
	if len(s.statusCounts) > 0 {
		msg["statuses"] = s.statusCounts
	}

	return msg
}

func sliceStats(sample, histogramBins []float64) message.Fields {
	return message.Fields{
		"sum":       floats.Sum(sample),
		"min":       floats.Min(sample),
		"max":       floats.Max(sample),
		"mean":      stat.Mean(sample, nil),
		"std_dev":   stat.StdDev(sample, nil),
		"histogram": stat.Histogram(nil, histogramBins, sample, nil),
	}
}

func (s *routeStats) reset() {
	s.durationMS = s.durationMS[:0]
	s.requestMB = s.requestMB[:0]
	s.responseMB = s.responseMB[:0]
	s.statusCounts = make(map[int]int)
}

func (l *Logger) resetStats() {
	for route, stats := range l.statsByRoute {
		stats.reset()
		l.statsByRoute[route] = stats
	}

	l.lastReset = time.Now()
	l.cacheIsFull = false
}
