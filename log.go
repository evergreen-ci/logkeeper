package logkeeper

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mongodb/amboy/logger"
	"github.com/mongodb/amboy/queue"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/level"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/send"
	"github.com/pkg/errors"
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
	if l <= 300 {
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
}

func GetSender(ctx context.Context, fn string) (send.Sender, error) {
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
			return nil, errors.Wrap(err, "creating the Splunk logger")
		}
		bufferedSender, err := send.NewBufferedSender(ctx, sender, send.BufferedSenderOptions{FlushInterval: interval, BufferSize: bufferCount})
		if err != nil {
			return nil, errors.Wrap(err, "creating buffered Splunk sender")
		}
		senders = append(senders, bufferedSender)
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

		senderQueue := queue.NewLocalLimitedSize(4, 2048)
		if err = senderQueue.Start(ctx); err != nil {
			return nil, errors.Wrap(err, "starting sender queue")
		}

		senders = append(senders, logger.MakeQueueSender(ctx, senderQueue, sender))
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
