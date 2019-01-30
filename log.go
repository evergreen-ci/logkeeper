package logkeeper

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/logger"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/level"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/send"
	"github.com/pkg/errors"
	"github.com/urfave/negroni"
)

const remoteAddrHeaderName = "X-Cluster-Client-Ip"

//  is a middleware handler that logs the request as it goes in and the response as it goes out.
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

func getLevel(l int) level.Priority {
	if l == http.StatusOK {
		return level.Debug
	}

	return level.Warning
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

	res := rw.(negroni.ResponseWriter)

	code := res.Status()

	grip.Log(getLevel(code), message.Fields{
		"method":   r.Method,
		"request":  reqID,
		"path":     r.URL.Path,
		"duration": time.Since(start),
		"action":   "completed",
		"status":   code,
		"remote":   remote,
		"outcome":  http.StatusText(code),
		"span":     time.Since(start).String(),
	})
}

func GetSender(queue amboy.Queue, fn string) (send.Sender, error) {
	const (
		name        = "logkeeper"
		interval    = 20 * time.Second
		bufferCount = 100
	)

	var (
		err       error
		sender    send.Sender
		senders   []send.Sender
		baseLevel = send.LevelInfo{Default: level.Info, Threshold: level.Debug}
	)

	// configure remote logging services first if the environment
	// variables are specified.

	if splunk := send.GetSplunkConnectionInfo(); splunk.Populated() {
		sender, err = send.NewSplunkLogger(name, splunk, send.LevelInfo{Default: level.Info, Threshold: level.Info})
		if err != nil {
			return nil, errors.Wrap(err, "problem creating the splunk logger")
		}

		senders = append(senders, send.NewBufferedSender(sender, interval, bufferCount))
	}

	if endpoint := os.Getenv("GRIP_SUMO_ENDPOINT"); endpoint != "" {
		sender, err = send.NewSumo(name, endpoint)
		if err != nil {
			return nil, errors.Wrap(err, "problem creating the sumo logic sender")
		}
		if err = sender.SetLevel(baseLevel); err != nil {
			return nil, errors.Wrap(err, "problem setting level for alert remote object")
		}

		senders = append(senders, send.NewBufferedSender(sender, interval, bufferCount))
	}

	// configure slack logger for alerts and panics

	if token := os.Getenv("GRIP_SLACK_CLIENT_TOKEN"); token != "" {
		channel := os.Getenv("GRIP_SLACK_CHANNEL")
		if channel == "" {
			channel = "#evergreen-ops-alerts"
		}
		if !strings.HasPrefix(channel, "#") {
			channel = "#" + channel
		}

		opts := &send.SlackOptions{
			Name:          name,
			Channel:       channel,
			AllFields:     true,
			BasicMetadata: true,
		}

		sender, err = send.NewSlackLogger(opts, token, send.LevelInfo{Default: level.Error, Threshold: level.Critical})
		if err != nil {
			return nil, errors.Wrap(err, "problem creating the slack sender")
		}

		// TODO use the amboy.Queue backed sender in this case.
		senders = append(senders, logger.MakeQueueSender(queue, sender))
	}

	// setup file logger, defaulting first to the system logger,
	// to standard output (or not at all) if specified, and
	// finally to the file as specified.

	if fn == "" {
		sender = getSystemLogger()
		if err = sender.SetLevel(send.LevelInfo{Default: level.Info, Threshold: level.Debug}); err != nil {
			return nil, errors.Wrap(err, "problem setting level for local system sender")
		}

		senders = append(senders, sender)
	} else if fn == "LOCAL" || fn == "--" || fn == "stdout" {
		sender, err = send.NewNativeLogger(name, baseLevel)
		if err != nil {
			return nil, errors.Wrap(err, "problem creating a native console logger")
		}

		senders = append(senders, sender)
	} else if (fn != "NONE") && (fn != "SKIP") {
		sender, err = send.NewFileLogger("logkeeper", fn, baseLevel)
		if err != nil {
			return nil, errors.Wrap(err, "problem creating a file logger")
		}

		senders = append(senders, sender)
	}

	return send.NewConfiguredMultiSender(senders...), nil
}
