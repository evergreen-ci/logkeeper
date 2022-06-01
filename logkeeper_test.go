package logkeeper

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/smartystreets/goconvey/convey/reporting"
	"go.mongodb.org/mongo-driver/mongo/bson"
	"go.mongodb.org/mongo-driver/mongo/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func resetDatabase(ctx context.Context) {
	grip.Error(db.DB().Drop(ctx))
}

func init() {
	reporting.QuietMode()
}

func TestLogKeeper(t *testing.T) {
	ctx := context.Background()

	client, err := mongo.NewClient(options.Client().ApplyURI("localhost:27017"))
	if err != nil {
		t.Fatal(err)
	}

	if err = client.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	db.SetClient(client)
	db.SetDBName("logkeeper_test")
	Convey("LogKeeper instance running on testdatabase", t, func() {
		lk := New(Options{MaxRequestSize: 1024 * 1024 * 10})
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
			r = newTestRequest(lk, "POST", "/build/"+buildId+"/test", map[string]interface{}{"test_filename": "myTestFileName", "command": "myCommand", "phase": "myPhase"})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			testId := data["id"].(string)
			testObjectID, err := primitive.ObjectIDFromHex(testId)
			So(err, ShouldBeNil)

			// Insert oversize log
			line := strings.Repeat("a", 2097152)
			now := time.Now().Unix()
			r = newTestRequest(lk, "POST", "/build/"+buildId+"/test/"+testId, [][]interface{}{{now, line}, {now, line}, {now, line}})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(len(data), ShouldBeGreaterThan, 0)

			// Test should have seq = 2
			test := &Test{}
			err = db.C("tests").FindOne(ctx, bson.M{"_id": testObjectID}).Decode(test)
			So(err, ShouldBeNil)
			So(test.Seq, ShouldEqual, 2)

			// Test should have two logs
			numLogs, err := db.C("logs").CountDocuments(ctx, bson.M{"test_id": testObjectID})
			So(err, ShouldBeNil)
			So(numLogs, ShouldEqual, 2)

			// First log should have two lines and seq=1
			// Second log should have one line and seq=2
			cur, err := db.C("logs").Find(ctx, bson.M{"test_id": testObjectID}, options.Find().SetSort("seq"))
			firstLog := true
			for cur.Next(ctx) {
				log := &Log{}
				So(cur.Decode(log), ShouldBeNil)

				if firstLog {
					So(len(log.Lines), ShouldEqual, 2)
					So(log.Seq, ShouldEqual, 1)
					firstLog = false
				} else {
					So(len(log.Lines), ShouldEqual, 1)
					So(log.Seq, ShouldEqual, 2)
				}
			}

			So(db.DB().Drop(ctx), ShouldBeNil)

			// Create build
			r = newTestRequest(lk, "POST", "/build", map[string]interface{}{"builder": "myBuilder", "buildnum": 123})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			buildId = data["id"].(string)

			// Insert oversize global log
			r = newTestRequest(lk, "POST", "/build/"+buildId, [][]interface{}{{now, line}, {now, line}, {now, line}})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(len(data), ShouldBeGreaterThan, 0)

			// Build should have seq = 2
			build := &LogKeeperBuild{}
			err = db.C("builds").FindOne(ctx, bson.M{"_id": buildId}).Decode(build)
			So(err, ShouldBeNil)
			So(build.Seq, ShouldEqual, 2)

			// Build should have two logs
			numLogs, err = db.C("logs").CountDocuments(ctx, bson.M{"build_id": buildId})
			So(err, ShouldBeNil)
			So(numLogs, ShouldEqual, 2)

			// First log should have two lines and seq=1
			// Second log should have one line and seq=2
			cur, err = db.C("logs").Find(ctx, bson.M{"build_id": buildId}, options.Find().SetSort("seq"))
			firstLog = true
			for cur.Next(ctx) {
				log := &Log{}
				So(cur.Decode(log), ShouldBeNil)

				if firstLog {
					So(len(log.Lines), ShouldEqual, 2)
					So(log.Seq, ShouldEqual, 1)
					firstLog = false
				} else {
					So(len(log.Lines), ShouldEqual, 1)
					So(log.Seq, ShouldEqual, 2)
				}
			}

			// Inserting oversize log line fails
			line = strings.Repeat("a", 4194305)
			r = newTestRequest(lk, "POST", "/build/"+buildId, [][]interface{}{{now, line}})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			So(w.Code, ShouldEqual, http.StatusBadRequest)

		})

		Convey("Adding the task id field will correctly insert it in the database", func() {
			// Create build and test
			r := newTestRequest(lk, "POST", "/build", map[string]interface{}{"builder": "myBuilder", "buildnum": 123})
			data := checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			buildId := data["id"].(string)
			r = newTestRequest(lk, "POST", "/build/"+buildId+"/test", map[string]interface{}{"test_filename": "myTestFileName", "command": "myCommand", "phase": "myPhase", "task_id": "abc123"})
			data = checkEndpointResponse(router, r, http.StatusCreated)
			So(data["id"], ShouldNotBeNil)
			testId := data["id"].(string)
			testObjectID, err := primitive.ObjectIDFromHex(testId)
			So(err, ShouldBeNil)

			test := &Test{}
			err = db.C("tests").FindOne(ctx, bson.M{"_id": testObjectID}).Decode(test)
			So(err, ShouldBeNil)
			So(test.Info, ShouldNotBeNil)
			So(test.Info["task_id"], ShouldEqual, "abc123")
		})

		// Clear database
		Reset(func() { resetDatabase(ctx) })
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
