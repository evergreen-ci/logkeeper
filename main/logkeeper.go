package main

import (
	"fmt"
	"github.com/codegangsta/negroni"
	"github.com/evergreen-ci/logkeeper"
	"github.com/phyber/negroni-gzip/gzip"
	"gopkg.in/mgo.v2"

	//"net/http"
)

func main() {
	port := 3000

	session, err := mgo.Dial("localhost:27017")
	if err != nil {
		fmt.Println(err)
		return
	}

	lk := logkeeper.New(session, logkeeper.Options{
		DB:  "buildlogs",
		URL: fmt.Sprintf("http://localhost:%v", port),
	})
	router := lk.NewRouter()
	n := negroni.Classic()
	n.Use(gzip.Gzip(gzip.DefaultCompression))
	n.UseHandler(router)

	n.Run(fmt.Sprintf(":%v", port))
}
