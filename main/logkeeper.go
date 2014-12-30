package main

import (
	"flag"
	"fmt"
	"github.com/codegangsta/negroni"
	"github.com/evergreen-ci/logkeeper"
	"github.com/phyber/negroni-gzip/gzip"
	"gopkg.in/mgo.v2"

	//"net/http"
)

func main() {
	var httpPort = flag.Int("port", 8080, "port to listen on for HTTP")
	var dbHost = flag.String("dbhost", "localhost:27017", "host/port to connect to DB server")
	flag.Parse()

	session, err := mgo.Dial(*dbHost)
	if err != nil {
		fmt.Println(err)
		return
	}

	lk := logkeeper.New(session, logkeeper.Options{
		DB:  "buildlogs",
		URL: fmt.Sprintf("http://localhost:%v", *httpPort),
	})
	router := lk.NewRouter()
	n := negroni.Classic()
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(router)

	n.Run(fmt.Sprintf(":%v", *httpPort))
}
