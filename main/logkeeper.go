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
	"github.com/phyber/negroni-gzip/gzip"
	"github.com/tychoish/grip"
	"github.com/tychoish/grip/level"
	"github.com/tychoish/grip/send"
	"github.com/tylerb/graceful"
	"gopkg.in/mgo.v2"
)

func main() {
	httpPort := flag.Int("port", 8080, "port to listen on for HTTP.")
	dbHost := flag.String("dbhost", "localhost:27017", "host/port to connect to DB server. Comma separated.")
	rsName := flag.String("rsName", "", "name of replica set that the DB instances belong to. "+
		"Leave empty for stand-alone and mongos instances.")
	maxRequestSize := flag.Int("maxRequestSize", 1024*1024*32,
		"maximum size for a request in bytes, defaults to 32 MB (in bytes)")
	flag.Parse()

	sendLogLevels := send.LevelInfo{
		Default:   level.Info,
		Threshold: level.Info,
	}

	sender, err := send.NewFileLogger("logkeeper", "logkeeperapp.log", sendLogLevels)
	grip.CatchEmergencyFatal(err)
	defer sender.Close()

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
	n.Use(lk.NewLogger())
	n.Use(negroni.NewRecovery())                 // part of negroni Classic settings
	n.Use(negroni.NewStatic(http.Dir("public"))) // part of negroni Classic settings
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(context.ClearHandler(router))

	grip.Noticeln("running logkeeper:", logkeeper.BuildRevision)
	graceful.Run(fmt.Sprintf(":%v", *httpPort), 10*time.Second, n)
}
