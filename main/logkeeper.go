package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/evergreen-ci/logkeeper"
	"github.com/phyber/negroni-gzip/gzip"
	"github.com/tylerb/graceful"
	"gopkg.in/mgo.v2"

	//"net/http"
)

func main() {
	var httpPort = flag.Int("port", 8080, "port to listen on for HTTP")
	var dbHost = flag.String("dbhost", "", "host/port to connect to DB server")
	flag.Parse()

	if *dbHost == "" {
		dbHostEnv := os.Getenv("MONGODB_URI")
		dbHostDefault := "localhost:27017"

		if dbHostEnv == "" {
			dbHost = &dbHostDefault
		} else {
			dbHost = &dbHostEnv
		}
	}

	fmt.Println("connecting a mongodb deployment at: ", *dbHost)
	session, err := mgo.Dial(*dbHost)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("connected to database; starting app.")

	lk := logkeeper.New(session, logkeeper.Options{
		DB:  "buildlogs",
		URL: fmt.Sprintf("http://localhost:%v", *httpPort),
	})
	router := lk.NewRouter()
	n := negroni.Classic()
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(router)

	fmt.Println("starting logkeeper now...")
	graceful.Run(fmt.Sprintf(":%v", *httpPort), 10*time.Second, n)
}
