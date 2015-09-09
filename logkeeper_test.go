package logkeeper

import (
	"bytes"
	"encoding/json"
	//"fmt"
	. "github.com/smartystreets/goconvey/convey"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"strings"
)

func TestLogKeeper(t *testing.T) {
	Convey("LogKeeper instance running on testdatabase", t, func() {
		session, err := mgo.Dial("localhost:27017")
		if err != nil {
			t.Fatal(err)
		}
		lk := New(session, Options{DB: "logkeeper_test"})
		db := session.DB("logkeeper_test")
		db.DropDatabase()
		router := lk.NewRouter()

		Convey("Call POST /build creates a build with the given builder/buildnum", func() {
			r := newTestRequest(lk, "POST", "/build/", map[string]interface{}{"builder": "poop", "buildnum": 123})
			data := checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			So(data["uri"], ShouldNotBeNil)
			originalId, originalURI := data["id"], data["uri"]

			// Call POST /build again,
			r = newTestRequest(lk, "POST", "/build/", map[string]interface{}{"builder": "poop", "buildnum": 123})
			data = checkEndpointResponse(router, r, http.StatusOK)
			So(data["id"], ShouldEqual, originalId)
			So(data["uri"], ShouldEqual, originalURI)
		})
		
		Convey("Logkeeper breaks oversize log into pieces", func() {
			// Create build and test
			r := newTestRequest(lk, "POST", "/build", map[string]interface{}{"builder": "myBuilder", "buildnum": 123})
			data := checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			buildId := data["id"].(string)
			r = newTestRequest(lk, "POST", "/build/" + buildId + "/test", map[string]interface{}{"test_filename": "myTestFileName", "command": "myCommand", "phase": "myPhase"})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			testId := data["id"].(string)

			// Insert oversize log
			line := strings.Repeat("a", 2097152)
			now := time.Now().Unix()
			r = newTestRequest(lk, "POST", "/build/" + buildId + "/test/" + testId, [][]interface{} {{now, line},{now, line},{now, line}})
			data = checkEndpointResponse(router, r, http.StatusCreated)

			// Test should have seq = 2
			test := &Test{}
			err := db.C("tests").Find(bson.M{"_id": bson.ObjectIdHex(testId)}).One(test)
			So(err, ShouldBeNil)
			So(test.Seq, ShouldEqual, 2)

			// Test should have two logs
			numLogs, err := db.C("logs").Find(bson.M{"test_id": bson.ObjectIdHex(testId)}).Count()
			So(err, ShouldBeNil)
			So(numLogs, ShouldEqual, 2)

			// Logs should have a total of three lines
			logs := db.C("logs").Find(bson.M{"test_id": bson.ObjectIdHex(testId)}).Iter()
			numLines := 0
			log := &Log{}
			for logs.Next(log) {
				numLines += len(log.Lines)
			}
			So(numLines, ShouldEqual, 3)

			// Clear database
			db.DropDatabase()

			// Create build
			r = newTestRequest(lk, "POST", "/build", map[string]interface{}{"builder": "myBuilder", "buildnum": 123})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			buildId = data["id"].(string)

			// Insert oversize global log
			r = newTestRequest(lk, "POST", "/build/" + buildId, [][]interface{} {{now, line},{now, line},{now, line}})
			data = checkEndpointResponse(router, r, http.StatusCreated)

			// Build should have seq = 2
			build := &LogKeeperBuild{}
			err = db.C("builds").Find(bson.M{"_id": bson.ObjectIdHex(buildId)}).One(build)
			So(err, ShouldBeNil)
			So(build.Seq, ShouldEqual, 2)

			// Build should have two logs
			numLogs, err = db.C("logs").Find(bson.M{"build_id": bson.ObjectIdHex(buildId)}).Count()
			So(err, ShouldBeNil)
			So(numLogs, ShouldEqual, 2)

			// Logs should have a total of three lines
			logs = db.C("logs").Find(bson.M{"build_id": bson.ObjectIdHex(buildId)}).Iter()
			numLines = 0
			log = &Log{}
			for logs.Next(log) {
				numLines += len(log.Lines)
			}
			So(numLines, ShouldEqual, 3)

			// Inserting oversize log line fails
			line = strings.Repeat("a", 4194305)
			r = newTestRequest(lk, "POST", "/build/" + buildId, [][]interface{} {{now, line}})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			So(w.Code, ShouldEqual, http.StatusBadRequest)

			// Clear database
			db.DropDatabase()
		})
		
	})
}

func checkEndpointResponse(router http.Handler, req *http.Request, expectedCode int) map[string]interface{} {
	w := httptest.NewRecorder()
	decoded := map[string]interface{}{}
	router.ServeHTTP(w, req)
	err := json.Unmarshal(w.Body.Bytes(), &decoded)
	So(err, ShouldBeNil)
	So(w.Code, ShouldEqual, expectedCode)
	return decoded
}

func newTestRequest(lk *logKeeper, method, path string, body interface{}) *http.Request {
	req, err := http.NewRequest(method, lk.opts.URL+path, nil)
	if err != nil {
		panic(err)
	}
	jsonbytes, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	req.Body = ioutil.NopCloser(bytes.NewReader(jsonbytes))
	return req
}
