package logkeeper

import (
	"math"
	"regexp"
	"time"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var colorRegex *regexp.Regexp = regexp.MustCompile(`([ \w]{2}\d{1,5}\|)`)

type LogLine []interface{}

type LogLineItem struct {
	LineNum   int
	Timestamp time.Time
	Data      string
	TestId    *primitive.ObjectID
}

// Global returns true if this log line comes from a global log, otherwise false (from a test log).
func (lli LogLineItem) Global() bool {
	return lli.TestId == nil
}

//A "log" doc looks like this:
/*
{
	"_id" : ObjectId("52e74ffd30dfa32be4877f47"),
	"build_id" : ObjectId("52e74d583ae7400f1a000001"),
	"test_id" : ObjectId("52e74ffb3ae74013e2000001"),
	"seq" : 1,
	"started" : null,
	"lines" : [
		[ ISODate("2014-01-28T06:36:43Z"), "log line 1..." ],
		[ ISODate("2014-01-28T06:36:43Z"), "log line 2..." ],
		[ ISODate("2014-01-28T06:36:43Z"), "log line 3..." ] //etc
      ...
	]
}*/

type Log struct {
	BuildId interface{}         `bson:"build_id"`
	TestId  *primitive.ObjectID `bson:"test_id"`
	Seq     int                 `bson:"seq"`
	Started *time.Time          `bson:"started"`
	Lines   []LogLine           `bson:"lines"`
}

func NewLogLine(data []interface{}) *LogLine {
	// timeField is generated client-side as the output of python's time.time(), which returns
	// seconds since epoch as a floating point number
	timeField, ok := data[0].(float64)
	if !ok {
		grip.Critical(message.Fields{
			"message": "unable to convert time field",
			"value":   data[0],
		})
		timeField = float64(time.Now().Unix())
	}

	// extract fractional seconds from the total time and convert to nanoseconds
	fractionalPart := timeField - math.Floor(timeField)
	nSecPart := int64(fractionalPart * float64(int64(time.Second)/int64(time.Nanosecond)))

	timeParsed := time.Unix(int64(timeField), nSecPart)
	return &LogLine{timeParsed, data[1].(string)}
}

func (s LogLine) Time() time.Time {
	return (s[0]).(time.Time)
}

func (s LogLine) Msg() string {
	return (s[1]).(string)
}

func (self *LogLineItem) Color() string {
	found := colorRegex.FindStringSubmatch(self.Data)
	if len(found) > 0 {
		return found[0]
	} else {
		return ""
	}
}

func (self *LogLineItem) OlderThanThreshold(previousItem interface{}) bool {
	if previousItem == nil {
		return true
	}

	if previousLogLine, ok := previousItem.(*LogLineItem); ok {
		diff := self.Timestamp.Sub(previousLogLine.Timestamp)
		if diff > 1*time.Second {
			return true
		} else {
			return false
		}
	}
	return true
}

// MergeLog takes two channels of LogLineItem and returns a single channel that feeds
// the result of merging the two input channels sorted by timestamp.
func MergeLog(logger1 chan *LogLineItem, logger2 chan *LogLineItem) chan *LogLineItem {
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
