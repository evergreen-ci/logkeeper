package model

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/pail"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2/bson"
)

// Test describes metadata of a test stored in pail-backed offline storage.
type Test struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BuildID       string `json:"build_id"`
	TaskID        string `json:"task_id"`
	TaskExecution int    `json:"execution"`
	Phase         string `json:"phase"`
	Command       string `json:"command"`
}

// NewTestID returns a new TestID with it's timestamp set to startTime.
// The ID is an ObjectID with its timestamp replaced with a nanosecond
// timestamp. It is represented as a hex string of 16 bytes. The first 8 bytes
// are the timestamp and replace the first 4 bytes of an ObjectID. The
// remaining 8 bytes are the rest of the ObjectID.
func NewTestID(startTime time.Time) string {
	objectID := bson.NewObjectId()
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(startTime.UnixNano()))
	buf = append(buf, []byte(objectID)[4:]...)

	return hex.EncodeToString(buf)
}

func (t *Test) key() string {
	return metadataKeyForTest(t.BuildID, t.ID)
}

func (t *Test) toJSON() ([]byte, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling test metadata")
	}

	return data, nil
}

// UploadTestMetadata uploads metadata for a new test to the pail-backed
// offline storage.
func (t *Test) UploadTestMetadata(ctx context.Context) error {
	data, err := t.toJSON()
	if err != nil {
		return nil
	}

	return errors.Wrapf(env.Bucket().Put(ctx, t.key(), bytes.NewReader(data)), "uploading metadata for test '%s'", t.ID)
}

// FindTestByID returns the test metadata for the given build ID and test ID
// from the pail-backed offline storage.
func FindTestByID(ctx context.Context, buildID string, testID string) (*Test, error) {
	reader, err := env.Bucket().Get(ctx, metadataKeyForTest(buildID, testID))
	if pail.IsKeyNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "getting test metadata for build '%s' and test '%s'", buildID, testID)
	}

	test := &Test{}
	if err = json.NewDecoder(reader).Decode(test); err != nil {
		return nil, errors.Wrapf(err, "parsing test metadata for build '%s' and test '%s'", buildID, testID)
	}

	return test, nil
}

// CheckBuildMetadata returns whether the metadata file exists for the given test.
func CheckTestMetadata(ctx context.Context, buildID string, testID string) (bool, error) {
	return checkMetadata(ctx, buildID, testID)
}

// FindTestsForBuild returns all of the test metadata for the given build ID
// from the pail-backed offline storage.
func FindTestsForBuild(ctx context.Context, buildID string) ([]Test, error) {
	iterator, err := env.Bucket().List(ctx, buildTestsPrefix(buildID))
	if err != nil {
		return nil, errors.Wrapf(err, "listing test keys for build '%s'", buildID)
	}

	testIDs := []string{}
	for iterator.Next(ctx) {
		if !strings.HasSuffix(iterator.Item().Name(), metadataFilename) {
			continue
		}

		testID, err := testIDFromKey(iterator.Item().Name())
		if err != nil {
			return nil, errors.Wrapf(err, "parsing test metadata key for build '%s'", buildID)
		}
		testIDs = append(testIDs, testID)
	}

	var wg sync.WaitGroup
	catcher := grip.NewBasicCatcher()
	tests := make([]Test, len(testIDs))
	for i, id := range testIDs {
		wg.Add(1)
		go func(testID string, idx int) {
			defer recovery.LogStackTraceAndContinue("finding test metadata for build from bucket")
			defer wg.Done()

			test, err := FindTestByID(ctx, buildID, testID)
			if err != nil {
				catcher.Add(err)
				return
			}
			tests[idx] = *test
		}(id, i)
	}
	wg.Wait()

	if catcher.HasErrors() {
		return nil, catcher.Resolve()
	}
	return tests, nil
}

// testIDTimestamp returns the timestamp encoded in the ID.
// If the ID is a legacy ObjectID then the timestamp will have second precision,
// otherwise it will have nanosecond precision.
func testIDTimestamp(id string) time.Time {
	if bson.IsObjectIdHex(id) {
		return bson.ObjectIdHex(id).Time()
	}

	bytes, err := hex.DecodeString(id)
	if err != nil {
		return time.Time{}
	}

	nSecs := binary.BigEndian.Uint64(bytes)
	return time.Unix(0, int64(nSecs))
}

// parseTestIDs parses test IDs from the buildKeys that correspond to test metadata files
// and sorts them by creation time.
func parseTestIDs(buildKeys []string) ([]string, error) {
	var testIDs []string
	for _, key := range buildKeys {
		if !strings.HasSuffix(key, metadataFilename) {
			continue
		}
		if !strings.Contains(key, "/tests/") {
			continue
		}
		testID, err := testIDFromKey(key)
		if err != nil {
			return nil, errors.Wrap(err, "getting test ID from metadata key")
		}
		testIDs = append(testIDs, testID)
	}

	sort.Slice(testIDs, func(i, j int) bool {
		return testIDTimestamp(testIDs[i]).Before(testIDTimestamp(testIDs[j]))
	})

	return testIDs, nil
}

// testExecutionWindow returns the time range from the creation of this test to
// the creation of the next test. If the given test ID is empty, the returned
// time range is unbounded. If there is no subsequent test then the end time is
// TimeRangeMax.
//
// Tests are expected to be run and, thus, logged sequentially. In cases where
// they overlap, the test execution window can exclude log lines from the test
// if the subsequent test begins before the previous test ended. To ensure that
// we capture all the log lines of a test, we do not filter the test chunks by
// by this time range. This does mean, though, that the build logs returned
// with the logs the test may be filtered by a time range shorter than that of
// the test itself—this behavior is okay since tests are expected to be run
// serially.
func testExecutionWindow(allTestIDs []string, testID string) (TimeRange, error) {
	tr := AllTime
	if testID == "" {
		return tr, nil
	}

	var found bool
	var testIndex int
	for i, id := range allTestIDs {
		if id == testID {
			found = true
			testIndex = i
		}
	}
	if !found {
		return tr, errors.Errorf("test '%s' was not found", testID)
	}

	tr.StartAt = testIDTimestamp(allTestIDs[testIndex]).Truncate(time.Millisecond)

	if testIndex < len(allTestIDs)-1 {
		tr.EndAt = testIDTimestamp(allTestIDs[testIndex+1]).Truncate(time.Millisecond)
	}

	return tr, nil
}

func testIDFromKey(path string) (string, error) {
	keyParts := strings.Split(path, "/")
	if strings.Contains(path, "/tests/") && len(keyParts) >= 5 {
		return keyParts[3], nil
	}
	return "", errors.Errorf("programmatic error: unexpected test ID prefix in path '%s'", path)
}

func metadataKeyForTest(buildID string, testID string) string {
	return fmt.Sprintf("%s%s", testPrefix(buildID, testID), metadataFilename)
}

func testPrefix(buildID, testID string) string {
	return fmt.Sprintf("%s%s/", buildTestsPrefix(buildID), testID)
}

func buildTestsPrefix(buildID string) string {
	return fmt.Sprintf("%stests/", buildPrefix(buildID))
}
