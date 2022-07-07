package logkeeper

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/mongodb/amboy/logger"
	"github.com/mongodb/amboy/queue"
	"github.com/mongodb/grip/level"
	"github.com/mongodb/grip/send"
	"github.com/pkg/errors"
)

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
			return nil, errors.Wrap(err, "creating the buffered Splunk logger")
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

		localQueue := queue.NewLocalLimitedSize(4, 2048)
		if err = localQueue.Start(ctx); err != nil {
			return nil, errors.Wrap(err, "starting local queue for Splunk logger")
		}
		senders = append(senders, logger.MakeQueueSender(ctx, localQueue, sender))
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
