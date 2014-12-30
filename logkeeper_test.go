package logkeeper

import (
	"bytes"
	"encoding/json"
	//"fmt"
	. "github.com/smartystreets/goconvey/convey"
	"gopkg.in/mgo.v2"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogKeeper(t *testing.T) {
	Convey("LogKeeper instance running on testdatabase", t, func() {
		session, err := mgo.Dial("localhost:27017")
		if err != nil {
			t.Fatal(err)
		}
		lk := New(session, Options{DB: "logkeeper_test"})
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
