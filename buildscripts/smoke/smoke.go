package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	port = 8080
)

var (
	sampleBuild = struct {
		Builder  string `json:"builder"`
		BuildNum int    `json:"buildnum"`
		TaskID   string `json:"task_id"`
	}{
		Builder:  "b0",
		BuildNum: rand.New(rand.NewSource(time.Now().UnixNano())).Int(),
		TaskID:   "t0",
	}

	sampleTest = struct {
		TestFilename string `json:"test_filename"`
		Command      string `json:"command"`
		Phase        string `json:"phase"`
		TaskID       string `json:"task_id"`
	}{
		TestFilename: "f0",
		Command:      "c0",
		Phase:        "p0",
		TaskID:       "t0",
	}

	globalLogs = [][]interface{}{
		{time.Date(2009, time.November, 10, 0, 0, 0, 0, time.UTC).Unix(), "hour 0 (global)"},
		{time.Date(2009, time.November, 10, 2, 0, 0, 0, time.UTC).Unix(), "hour 2 (global)"},
	}

	testLogs = [][]interface{}{
		{time.Date(2009, time.November, 10, 1, 0, 0, 0, time.UTC).Unix(), "hour 1 (test)"},
	}

	expectedAllLogs  = "hour 0 (global)\nhour 1 (test)\nhour 2 (global)\n"
	expectedTestLogs = "hour 1 (test)\n"
)

func getStatus() error {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/status", port))
	if err != nil {
		return errors.Wrap(err, "making request")
	}
	return resp.Body.Close()
}

func createBuild() (string, error) {
	body, _ := json.Marshal(sampleBuild)
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/build", port), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", errors.Wrap(err, "making request")
	}
	defer resp.Body.Close()

	target := struct {
		Id string `json:"id"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return "", errors.Wrap(err, "unmarshaling json response")
	}

	return target.Id, nil
}

func createTest(buildID string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"test_filename": "f0",
		"command":       "c0",
		"phase":         "p0",
		"task_id":       "t0",
	})
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/build/%s/test", port, buildID), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", errors.Wrap(err, "making request")
	}
	defer resp.Body.Close()

	target := struct {
		Id string `json:"id"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return "", errors.Wrap(err, "unmarshaling json response")
	}

	return target.Id, nil
}

func uploadGlobalLog(buildID string) error {
	body, _ := json.Marshal(globalLogs)
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/build/%s", port, buildID), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return errors.Wrap(err, "making request")
	}
	return resp.Body.Close()
}

func uploadTestLog(buildID, testID string) error {
	body, _ := json.Marshal(testLogs)
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/build/%s/test/%s", port, buildID, testID), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return errors.Wrap(err, "making request")
	}
	return resp.Body.Close()
}

func getAllLogs(buildID string) (string, error) {
	return getLogs(fmt.Sprintf("http://localhost:%d/build/%s/all?raw=1", port, buildID))
}

func getTestLogs(buildID, testID string) (string, error) {
	return getLogs(fmt.Sprintf("http://localhost:%d/build/%s/test/%s?raw=1", port, buildID, testID))
}

func getLogs(route string) (string, error) {
	resp, err := http.Get(route)
	if err != nil {
		return "", errors.Wrap(err, "making request")
	}
	defer resp.Body.Close()
	builder := strings.Builder{}
	_, err = io.Copy(&builder, resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "reading response body")
	}
	return builder.String(), nil
}

func main() {
	if err := getStatus(); err != nil {
		fmt.Fprint(os.Stderr, errors.Wrap(err, "getting status"))
		os.Exit(1)
	}

	buildID, err := createBuild()
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrap(err, "creating build"))
		os.Exit(1)
	}

	testID, err := createTest(buildID)
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrap(err, "creating test"))
		os.Exit(1)
	}

	if err := uploadGlobalLog(buildID); err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrap(err, "uploading global log"))
		os.Exit(1)
	}

	if err := uploadTestLog(buildID, testID); err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrap(err, "uploading test log"))
		os.Exit(1)
	}

	allLogs, err := getAllLogs(buildID)
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrap(err, "getting all logs"))
		os.Exit(1)
	}
	if expectedAllLogs != allLogs {
		fmt.Fprintln(os.Stderr, "all logs received didn't match expected output")
		os.Exit(1)
	}

	testLogs, err := getTestLogs(buildID, testID)
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrap(err, "getting test logs"))
		os.Exit(1)
	}
	if expectedTestLogs != testLogs {
		fmt.Printf("got '%s' expected: '%s'", testLogs, expectedTestLogs)
		fmt.Fprintln(os.Stderr, "test logs received didn't match expected output")
		os.Exit(1)
	}

}
