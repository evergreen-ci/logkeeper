package storage

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/mgo.v2/bson"
)

// Test describes metadata of a test stored in pail-backed offline storage.
type Test struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BuildID string `json:"build_id"`
	TaskID  string `json:"task_id"`
	Phase   string `json:"phase"`
	Command string `json:"command"`
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

// testIDTimestamp returns the timestamp encoded in the ID.
// If the TestID is a legacy ObjectID then the timestamp will have second precision,
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

func (t *Test) key() string {
	return metadataKeyForTest(t.BuildID, t.ID)
}

func metadataKeyForTest(buildID string, testID string) string {
	return fmt.Sprintf("%s%s", testPrefix(buildID, testID), metadataFilename)
}

func (t *Test) toJSON() ([]byte, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling test metadata")
	}

	return data, nil
}

func testIDFromKey(path string) (string, error) {
	keyParts := strings.Split(path, "/")
	if strings.Contains(path, "/tests/") && len(keyParts) >= 5 {
		return keyParts[3], nil
	}
	return "", errors.Errorf("programmatic error: unexpected test ID prefix in path '%s'", path)
}

func testPrefix(buildID, testID string) string {
	return fmt.Sprintf("%s%s/", buildTestsPrefix(buildID), testID)
}

func buildTestsPrefix(buildID string) string {
	return fmt.Sprintf("%stests/", buildPrefix(buildID))
}
