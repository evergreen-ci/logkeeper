package model

import (
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	// LogsCollection is the name of the logs collection in the database.
	LogsCollection = "logs"
)

var colorRegex *regexp.Regexp = regexp.MustCompile(`([ \w]{2}\d{1,5}\|)`)

//A "log" doc looks like this:
/*
{
	"_id" : ObjectId("52e74ffd30dfa32be4877f47"),
	"build_id" : "52e74d583ae7400f1a000001",
	"test_id" : "52e74ffb3ae74013e2000001",
	"seq" : 1,
	"started" : null,
	"lines" : [
		[ ISODate("2014-01-28T06:36:43Z"), "log line 1..." ],
		[ ISODate("2014-01-28T06:36:43Z"), "log line 2..." ],
		[ ISODate("2014-01-28T06:36:43Z"), "log line 3..." ] //etc
      ...
	]
}*/

// Log is a slice of lines and metadata about them.
type Log struct {
	BuildId string     `bson:"build_id"`
	TestId  *TestID    `bson:"test_id"`
	Seq     int        `bson:"seq"`
	Started *time.Time `bson:"started"`
	Lines   []LogLine  `bson:"lines"`
}

// RemoveLogsForBuild removes all logs created by the specificed build.
func RemoveLogsForBuild(buildID string) (int, error) {
	db, closeSession := db.DB()
	defer closeSession()

	info, err := db.C(LogsCollection).RemoveAll(bson.M{"build_id": buildID})
	if err != nil {
		return 0, errors.Wrapf(err, "deleting logs for build '%s'", buildID)
	}

	return info.Removed, nil
}

func findLogsInWindow(query bson.M, sort []string, minTime, maxTime *time.Time) chan *LogLineItem {
	outputLog := make(chan *LogLineItem)
	logItem := &Log{}

	go func() {
		db, closeSession := db.DB()
		defer closeSession()

		defer close(outputLog)
		lineNum := 0
		log := db.C("logs").Find(query).Sort(sort...).Iter()
		for log.Next(logItem) {
			for _, line := range logItem.Lines {
				if minTime != nil && line.Time.Before(*minTime) {
					continue
				}
				if maxTime != nil && line.Time.After(*maxTime) {
					continue
				}
				outputLog <- &LogLineItem{
					LineNum:   lineNum,
					Timestamp: line.Time,
					Data:      line.Msg,
					TestId:    logItem.TestId,
				}
				lineNum++
			}
		}
	}()
	return outputLog
}

// AllLogs returns a channel with all build and test logs for the build merged together by timestamp.
func AllLogs(buildID string) (chan *LogLineItem, error) {
	globalLogs := findLogsInWindow(bson.M{"build_id": buildID, "test_id": nil}, []string{"seq"}, nil, nil)
	testLogs := findLogsInWindow(bson.M{"build_id": buildID, "test_id": bson.M{"$ne": nil}}, []string{"build_id", "started"}, nil, nil)
	return MergeLogChannels(testLogs, globalLogs), nil
}

// MergedTetsLogs returns a channel with the test's logs merged with the concurrent global logs.
func MergedTestLogs(test *Test) (chan *LogLineItem, error) {
	globalLogs, err := findGlobalLogsDuringTest(test)
	if err != nil {
		return nil, errors.Wrap(err, "finding global logs during test")
	}
	testLogs := findLogsInWindow(bson.M{"build_id": test.BuildId, "test_id": test.Id}, []string{"seq"}, nil, nil)
	return MergeLogChannels(testLogs, globalLogs), nil
}

func findGlobalLogsDuringTest(test *Test) (chan *LogLineItem, error) {
	db, closeSession := db.DB()
	defer closeSession()

	var globalSeqFirst, globalSeqLast *int

	minTime, maxTime, err := test.GetExecutionWindow()
	if err != nil {
		return nil, errors.Wrap(err, "getting execution window")
	}

	// Find the first global log entry before this test started.
	// This may not actually contain any global log lines during the test run, if the entry returned
	// by this query comes from after the *next* test stared.
	firstGlobalLog := &Log{}
	err = db.C("logs").Find(bson.M{"build_id": test.BuildId, "test_id": nil, "started": bson.M{"$lt": minTime}}).Sort("-seq").Limit(1).One(firstGlobalLog)
	if err != nil {
		if err != mgo.ErrNotFound {
			return nil, err
		}
		// There are no global entries after this test started.
		globalSeqFirst = nil
	} else {
		globalSeqFirst = &firstGlobalLog.Seq
	}

	lastGlobalLog := &Log{}

	if maxTime != nil {
		// Find the last global log entry that covers this test. This may return a global log entry
		// that started before the test itself.
		err = db.C("logs").Find(bson.M{"build_id": test.BuildId, "test_id": nil, "started": bson.M{"$lt": maxTime}}).Sort("-seq").Limit(1).One(lastGlobalLog)
		if err != nil {
			if err != mgo.ErrNotFound {
				return nil, err
			}
			globalSeqLast = nil
		} else {
			globalSeqLast = &lastGlobalLog.Seq
		}
	}

	var globalLogsSeq bson.M
	if globalSeqFirst == nil {
		globalLogsSeq = bson.M{"$gte": test.Seq}
	} else {
		globalLogsSeq = bson.M{"$gte": *globalSeqFirst}
	}

	if globalSeqLast != nil {
		globalLogsSeq["$lte"] = *globalSeqLast
	}

	return findLogsInWindow(bson.M{"build_id": test.BuildId, "test_id": nil, "seq": globalLogsSeq}, []string{"seq"}, &minTime, maxTime), nil
}

// LogLine is a single line and its timestamp.
type LogLine struct {
	Time time.Time
	Msg  string
}

// LogChunk is a grouping of lines.
type LogChunk []LogLine

// GroupLines breaks up a slice of LogLines into chunks. The sum of the sizes of all messages in each chunk is
// less than or equal to maxSize.
func GroupLines(lines []LogLine, maxSize int) ([]LogChunk, error) {
	var chunks []LogChunk
	var currentChunk LogChunk

	logChars := 0
	for _, line := range lines {
		if len(line.Msg) > maxSize {
			return nil, errors.New("Log line exceeded 4MB")
		}

		if len(line.Msg)+logChars > maxSize {
			logChars = 0
			chunks = append(chunks, currentChunk)
			currentChunk = LogChunk{}
		}

		logChars += len(line.Msg)
		currentChunk = append(currentChunk, line)
	}

	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	return chunks, nil
}

// InsertLogChunks inserts log chunks as Logs in the logs collection.
func InsertLogChunks(buildID string, testID *TestID, lastSequence int, chunks []LogChunk) error {
	for i, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}

		logEntry := Log{
			BuildId: buildID,
			TestId:  testID,
			Seq:     lastSequence - len(chunks) + i + 1,
			Lines:   chunk,
			Started: &chunk[0].Time,
		}

		if err := logEntry.Insert(); err != nil {
			return errors.Wrap(err, "inserting log entry")
		}
	}

	return nil
}

// Insert inserts the log into the logs collection.
func (l *Log) Insert() error {
	db, closeSession := db.DB()
	defer closeSession()

	return errors.Wrap(db.C("logs").Insert(l), "inserting log entry")

}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (ll *LogLine) UnmarshalJSON(data []byte) error {
	var line []interface{}
	if err := json.Unmarshal(data, &line); err != nil {
		return errors.Wrap(err, "unmarshaling line into array")
	}

	// timeField is generated client-side as the output of python's time.time(), which returns
	// seconds since epoch as a floating point number
	timeField, ok := line[0].(float64)
	if !ok {
		grip.Critical(message.Fields{
			"message": "unable to convert time field",
			"value":   line[0],
		})
		timeField = float64(time.Now().Unix())
	}
	// extract fractional seconds from the total time and convert to nanoseconds
	fractionalPart := timeField - math.Floor(timeField)
	nSecPart := int64(fractionalPart * float64(int64(time.Second)/int64(time.Nanosecond)))

	ll.Time = time.Unix(int64(timeField), nSecPart)
	ll.Msg = line[1].(string)

	return nil
}

// GetBSON implements the bson.Getter interface.
// When a LogLine is marshalled to BSON the driver will marshal the output
// of this function instead of the struct.
func (ll LogLine) GetBSON() (interface{}, error) {
	return []interface{}{ll.Time, ll.Msg}, nil
}

// SetBSON implements the bson.Setter interface.
// When a LogLine is unmarshalled from BSON the driver will call this function to
// unmarshal into the LogLine.
func (ll *LogLine) SetBSON(raw bson.Raw) error {
	line := []interface{}{}
	if err := raw.Unmarshal(&line); err != nil {
		return &bson.TypeError{
			Kind: raw.Kind,
			Type: reflect.TypeOf(line),
		}
	}
	if len(line) < 2 {
		return errors.Errorf("line was of unexpected length %d", len(line))
	}

	time, ok := line[0].(time.Time)
	if !ok {
		return errors.Errorf("timestamp was of unexpected type %T", line[0])
	}

	msg, ok := line[1].(string)
	if !ok {
		return errors.Errorf("message was of unexpected type '%T'", line[1])
	}

	ll.Time = time.UTC()
	ll.Msg = msg
	return nil
}

// LogLineItem represents a single line in a log.
type LogLineItem struct {
	LineNum   int
	Timestamp time.Time
	Data      string
	TestId    *TestID
}

// Global returns true if this log line comes from a global log, otherwise false (from a test log).
func (lli LogLineItem) Global() bool {
	return lli.TestId == nil
}

func (item *LogLineItem) Color() string {
	found := colorRegex.FindStringSubmatch(item.Data)
	if len(found) > 0 {
		return found[0]
	} else {
		return ""
	}
}

func (item *LogLineItem) OlderThanThreshold(previousItem interface{}) bool {
	if previousItem == nil {
		return true
	}

	if previousLogLine, ok := previousItem.(*LogLineItem); ok {
		diff := item.Timestamp.Sub(previousLogLine.Timestamp)
		if diff > 1*time.Second {
			return true
		} else {
			return false
		}
	}
	return true
}

// MergeLogChannels takes two channels of LogLineItem and returns a single channel that feeds
// the result of merging the two input channels sorted by timestamp.
func MergeLogChannels(logger1 chan *LogLineItem, logger2 chan *LogLineItem) chan *LogLineItem {
	outputChan := make(chan *LogLineItem)
	go func() {
		next1, ok1 := <-logger1
		next2, ok2 := <-logger2
		for {
			if !ok1 && !ok2 { // both channels are empty - so stop.
				close(outputChan)
				return
			}
			if !ok2 { // only channel 1 had a value, so send that to output
				outputChan <- next1
				next1, ok1 = <-logger1 // get the next item from chan 1
			} else if !ok1 { // only channel 2 had a value, so send that to output
				outputChan <- next2
				next2, ok2 = <-logger2 // get the next item from chan 2
			} else {
				if next1.Timestamp.Before(next2.Timestamp) {
					outputChan <- next1
					next1, ok1 = <-logger1
				} else {
					outputChan <- next2
					next2, ok2 = <-logger2
				}
			}
		}
	}()
	return outputChan
}
