package main

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/evergreen-ci/logkeeper"
	"github.com/gorilla/context"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/level"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/send"
	"github.com/phyber/negroni-gzip/gzip"
	"github.com/tylerb/graceful"
	"gopkg.in/mgo.v2"
)

func recoverAndLogStackTrace() {
	if p := recover(); p != nil {
		panicMsg, ok := p.(string)
		if !ok {
			panicMsg = fmt.Sprintf("%+v", panicMsg)
		}
		m := message.NewStackFormatted(1, "encountered panic '%s' at top level; recovering trace:", panicMsg)
		grip.Alert(m)

		r := m.Raw().(message.StackTrace)
		for idx, f := range r.Frames {
			grip.Criticalf("call #%d\n\t%s\n\t\t%s:%d", idx, f.Function, f.File, f.Line)
		}

		grip.EmergencyFatalf("hit panic '%s' at top level; exiting", panicMsg)
	}
}

func main() {
	defer recoverAndLogStackTrace()

	httpPort := flag.Int("port", 8080, "port to listen on for HTTP.")
	dbHost := flag.String("dbhost", "localhost:27017", "host/port to connect to DB server. Comma separated.")
	rsName := flag.String("rsName", "", "name of replica set that the DB instances belong to. "+
		"Leave empty for stand-alone and mongos instances.")
	logPath := flag.String("logpath", "logkeeperapp.log", "path to log file")
	maxRequestSize := flag.Int("maxRequestSize", 1024*1024*32,
		"maximum size for a request in bytes, defaults to 32 MB (in bytes)")
	flag.Parse()

	sendLogLevels := send.LevelInfo{
		Default:   level.Info,
		Threshold: level.Info,
	}

	sender, err := send.NewFileLogger("logkeeper", *logPath, sendLogLevels)
	grip.CatchEmergencyFatal(err)
	defer sender.Close()

	splunkInfo := send.GetSplunkConnectionInfo()
	if splunkInfo.Populated() {
		var splunk send.Sender
		splunk, err = send.NewSplunkLogger("logkeeper", splunkInfo, sendLogLevels)
		if err == nil {
			sender = send.NewConfiguredMultiSender(sender, send.NewBufferedSender(splunk, 20*time.Second, 100))
		}
		grip.Warning(err)
	}

	grip.CatchEmergencyFatal(grip.SetSender(sender))

	dialInfo := mgo.DialInfo{
		Addrs: strings.Split(*dbHost, ","),
	}

	if *rsName != "" {
		dialInfo.ReplicaSetName = *rsName
	}

	session, err := mgo.DialWithInfo(&dialInfo)
	grip.CatchEmergencyFatal(err)

	lk := logkeeper.New(session, logkeeper.Options{
		DB:             "buildlogs",
		URL:            fmt.Sprintf("http://localhost:%v", *httpPort),
		MaxRequestSize: *maxRequestSize,
	})

	router := lk.NewRouter()
	n := negroni.New()
	n.Use(logkeeper.NewLogger())
	n.Use(negroni.NewStatic(http.Dir("public"))) // part of negroni Classic settings
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(context.ClearHandler(router))

	grip.Noticeln("running logkeeper:", logkeeper.BuildRevision)
	graceful.Run(fmt.Sprintf(":%v", *httpPort), 10*time.Second, n)
}
