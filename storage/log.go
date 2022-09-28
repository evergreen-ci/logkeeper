package storage

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/utility"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
)

var colorRegex *regexp.Regexp = regexp.MustCompile(`([ \w]{2}\d{1,5}\|)`)

// LogLineItem represents a single line in a log.
type LogLineItem struct {
	Timestamp time.Time
	Data      string
	Global    bool
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (ll *LogLineItem) UnmarshalJSON(data []byte) error {
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

	ll.Timestamp = time.Unix(int64(timeField), nSecPart)
	ll.Data = line[1].(string)

	return nil
}

// LogChunk is a grouping of lines.
type LogChunk []LogLineItem

// GroupLines breaks up a slice of LogLineItems into chunks. The sum of the sizes of lines' Data in each chunk is
// less than or equal to maxSize.
func GroupLines(lines []LogLineItem, maxSize int) ([]LogChunk, error) {
	var chunks []LogChunk
	var currentChunk LogChunk

	logChars := 0
	for _, line := range lines {
		if len(line.Data) > maxSize {
			return nil, errors.New("Log line exceeded 4MB")
		}

		if len(line.Data)+logChars > maxSize {
			logChars = 0
			chunks = append(chunks, currentChunk)
			currentChunk = LogChunk{}
		}

		logChars += len(line.Data)
		currentChunk = append(currentChunk, line)
	}

	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	return chunks, nil
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

// LogChunkInfo describes a chunk of log lines stored in pail-backed offline
// storage.
type LogChunkInfo struct {
	BuildID  string
	TestID   string
	NumLines int
	Start    time.Time
	End      time.Time
}

func (info *LogChunkInfo) key() string {
	var prefix string
	if info.TestID != "" {
		prefix = testPrefix(info.BuildID, info.TestID)
	} else {
		prefix = buildPrefix(info.BuildID)
	}
	return fmt.Sprintf("%s%d_%d_%d", prefix, info.Start.UnixNano(), info.End.UnixNano(), info.NumLines)
}

func (info *LogChunkInfo) fromKey(path string) error {
	var keyName string
	keyParts := strings.Split(path, "/")
	if strings.Contains(path, "/tests/") {
		if len(keyParts) < 5 {
			return errors.Errorf("invalid chunk key '%s'", path)
		}
		info.BuildID = keyParts[1]
		info.TestID = keyParts[3]
		keyName = keyParts[4]
	} else {
		if len(keyParts) < 3 {
			return errors.Errorf("invalid chunk key '%s'", path)
		}
		info.BuildID = keyParts[1]
		keyName = keyParts[2]
	}

	nameParts := strings.Split(keyName, "_")
	startNanos, err := strconv.ParseInt(nameParts[0], 10, 64)
	if err != nil {
		return errors.Wrap(err, "parsing start time")
	}
	info.Start = time.Unix(0, startNanos).UTC()

	endNanos, err := strconv.ParseInt(nameParts[1], 10, 64)
	if err != nil {
		return errors.Wrap(err, "parsing end time")
	}
	info.End = time.Unix(0, endNanos).UTC()

	numLines, err := strconv.ParseInt(nameParts[2], 10, 64)
	if err != nil {
		return errors.Wrap(err, "parsing num lines")
	}
	info.NumLines = int(numLines)

	return nil
}

func (info *LogChunkInfo) fromLogChunk(buildID string, testID string, logChunk LogChunk) error {
	if len(logChunk) == 0 {
		return errors.New("log chunk must contain at least one line")
	}
	minTime := TimeRangeMax
	maxTime := TimeRangeMin
	for _, logLine := range logChunk {
		if logLine.Timestamp.Before(minTime) {
			minTime = logLine.Timestamp
		}
		if logLine.Timestamp.After(maxTime) {
			maxTime = logLine.Timestamp
		}
	}
	info.BuildID = buildID
	info.TestID = testID
	info.NumLines = len(logChunk)
	info.Start = minTime
	info.End = maxTime
	return nil
}

func parseLogLineString(data string) (LogLineItem, error) {
	ts, err := strconv.ParseInt(strings.TrimSpace(data[3:23]), 10, 64)
	if err != nil {
		return LogLineItem{}, errors.Wrap(err, "parsing log line timestamp")
	}

	return LogLineItem{
		Timestamp: time.Unix(0, ts*1e6).UTC(),
		// We need to Trim the newline here because Logkeeper doesn't
		// expect newlines to be included in the LogLineItem.
		Data: strings.TrimRight(data[23:], "\n"),
	}, nil
}

func makeLogLineStrings(logLine LogLineItem) []string {
	singleLines := strings.Split(logLine.Data, "\n")
	logLines := make([]string, 0, len(singleLines))
	for _, line := range singleLines {
		logLines = append(logLines, fmt.Sprintf("  0%20d%s\n", utility.UnixMilli(logLine.Timestamp), line))
	}
	return logLines
}
