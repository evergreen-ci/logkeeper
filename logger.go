package logkeeper

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
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
	bytesPerMB           = 1024 * 1024
)

var (
	durationBins = []float64{0, 250, 500, 1000, 5000, 30000, 60000, math.MaxFloat64}
	sizeBins     = []float64{0, 0.5, 1, 5, 10, 50, math.MaxFloat64}
)

// Logger is a middleware handler that aggregates statistics on responses. Route statistics are periodically logged.
// If a handler panics Logger will recover the panic and log its error.
type Logger struct {
	// ids is a channel producing unique, auto-incrementing request ids.
	// A request's id can be extracted from its context with getCtxRequestId.
	ids chan int

	newResponses chan routeResponse
	statsByRoute map[string]routeStats
	cacheIsFull  bool
	lastReset    time.Time
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

// NewLogger returns a new Logger instance and starts its background goroutines.
func NewLogger(ctx context.Context) *Logger {
	l := &Logger{
		ids:          make(chan int, chanBufferSize),
		newResponses: make(chan routeResponse, chanBufferSize),
		statsByRoute: make(map[string]routeStats),
		lastReset:    time.Now(),
	}

	go l.incrementIDLoop(ctx)
	go l.responseLoggerLoop(ctx, loggerStatsInterval)

	return l
}

// Middleware returns a handler that incorporates the response into its response cache.
// If next panics the panic is recovered and logged.
func (l *Logger) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
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

		next.ServeHTTP(rw, r)

		grip.ErrorWhen(
			sometimes.Percent(logErrorPercentage),
			message.WrapError(
				l.addToResponseBuffer(rw, r),
				message.Fields{
					"message": "adding response to buffer",
					"path":    r.URL.Path,
				},
			),
		)
	})
}

func (l *Logger) responseLoggerLoop(ctx context.Context, tickerInterval time.Duration) {
	defer recovery.LogStackTraceAndContinue("logger loop")

	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.flushStats()
		case response := <-l.newResponses:
			l.recordResponse(response)

			if l.cacheIsFull {
				l.flushStats()
				ticker.Reset(tickerInterval)
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

func (l *Logger) addToResponseBuffer(rw http.ResponseWriter, r *http.Request) error {
	route := mux.CurrentRoute(r)
	if r == nil {
		return errors.New("request didn't contain a route")
	}
	path, err := route.GetPathTemplate()
	if err != nil {
		return errors.Wrap(err, "getting path template")
	}
	methods, err := route.GetMethods()
	if err != nil {
		return errors.Wrap(err, "getting methods")
	}

	writer := negroni.NewResponseWriter(rw)
	select {
	case l.newResponses <- routeResponse{
		route:        fmt.Sprintf("[%s] %s", strings.Join(methods, ", "), path),
		duration:     time.Since(getRequestStartAt(r.Context())),
		status:       writer.Status(),
		responseSize: writer.Size(),
		requestSize:  int(r.ContentLength),
	}:
		return nil
	default:
		return errors.New("response buffer is full")
	}
}

func (l *Logger) recordResponse(response routeResponse) {
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
		if stats.count() == 0 {
			continue
		}

		msg := stats.makeMessage()
		msg["route"] = route
		msg["interval"] = time.Since(l.lastReset)

		grip.Info(msg)
	}

	l.resetStats()
}

func (s *routeStats) count() int {
	return len(s.durationMS)
}

func (s *routeStats) makeMessage() message.Fields {
	msg := message.Fields{
		"message":  "route stats",
		"count":    s.count(),
		"statuses": s.statusCounts,
	}

	msg["service_time_ms"] = sliceStats(s.durationMS, durationBins)
	msg["response_size_mb"] = sliceStats(s.responseMB, sizeBins)
	msg["request_size_mb"] = sliceStats(s.requestMB, sizeBins)

	return msg
}

func sliceStats(sample, histogramBins []float64) message.Fields {
	if len(sample) == 0 {
		return message.Fields{}
	}
	sort.Float64s(sample)

	min := sample[0]
	max := sample[len(sample)-1]
	if len(histogramBins) == 0 || histogramBins[0] > min || histogramBins[len(histogramBins)-1] <= max {
		return message.Fields{}
	}

	// Only calculate the standard deviation if the sample size is greater
	// than 1, otherwise set it to 0. This avoids setting the field to NaN,
	// which cannot be marshalled into JSON, and ensures that all of the
	// stats logs are written correctly to Splunk.
	var stdDev float64
	if len(sample) > 1 {
		stdDev = stat.StdDev(sample, nil)
	}

	return message.Fields{
		"sum":       floats.Sum(sample),
		"min":       min,
		"max":       max,
		"mean":      stat.Mean(sample, nil),
		"std_dev":   stdDev,
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
