package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/utility"
	"github.com/pkg/errors"
)

var loggerRegex *regexp.Regexp = regexp.MustCompile(`([ \w]{2}\d{1,5}\|)`)

// LogLineItem represents a single line in a log.
type LogLineItem struct {
	Timestamp time.Time
	Data      string
	Global    bool
}

// UnmarshalLogJSON unmarshals log lines from JSON into a slice of LogLineItem.
// Unmarshalling directly is more efficient than implementing the Unmarshaller interface.
func UnmarshalLogJSON(r io.Reader) ([]LogLineItem, error) {
	var lines []LogLineItem

	dec := json.NewDecoder(r)
	firstToken, err := dec.Token()
	if err != nil {
		return lines, errors.New("reading opening bracket")
	}
	if delim, ok := firstToken.(json.Delim); !ok || delim != '[' {
		return lines, errors.Errorf("unexpected first token '%v' of type '%T'", firstToken, firstToken)
	}

	for dec.More() {
		var line []interface{}
		if err := dec.Decode(&line); err != nil {
			return nil, errors.Wrap(err, "decoding line")
		}
		if len(line) != 2 {
			return lines, errors.Errorf("line had unexpected number of elements %d", len(line))
		}

		timestamp, ok := line[0].(float64)
		if !ok {
			return lines, errors.Errorf("unexpected timestamp token '%v' of type '%v'", line[0], line[0])
		}
		data, ok := line[1].(string)
		if !ok {
			return lines, errors.Errorf("unexpected data token '%v' of type '%v'", line[1], line[1])
		}

		// Extract fractional seconds from the total time and convert to
		// nanoseconds.
		fractionalPart := timestamp - math.Floor(timestamp)
		nSecPart := int64(fractionalPart * float64(int64(time.Second)/int64(time.Nanosecond)))

		lines = append(lines, LogLineItem{
			Timestamp: time.Unix(int64(timestamp), nSecPart),
			Data:      data,
		})
	}

	lastToken, err := dec.Token()
	if err != nil {
		return lines, errors.New("reading closing bracket")
	}
	if delim, ok := lastToken.(json.Delim); !ok || delim != ']' {
		return lines, errors.Errorf("unexpected last token '%v' of type '%T'", lastToken, lastToken)
	}

	nextToken, err := dec.Token()
	if err != io.EOF {
		return lines, errors.Errorf("expected end of file, got '%v', type '%T'", nextToken, nextToken)
	}

	return lines, nil
}

// LoggerName returns the logger name for this line so it can be assigned a
// unique color when the log is displayed in the HTML view.
func (item *LogLineItem) LoggerName() string {
	found := loggerRegex.FindStringSubmatch(item.Data)
	if len(found) > 0 {
		return found[0]
	} else {
		return ""
	}
}

// OneSecondNewer returns if this line's timestamp is greater than one second
// newer than the previous line's timestamp.
func (item *LogLineItem) OneSecondNewer(previousItem interface{}) bool {
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

// DownloadLogLines returns log lines for a given build ID and test ID. If the
// test ID is empty, this will return all logs lines in the build.
func DownloadLogLines(ctx context.Context, buildID string, testID string) (chan *LogLineItem, error) {
	buildKeys, err := getBuildKeys(ctx, buildID)
	if err != nil {
		return nil, errors.Wrapf(err, "getting keys for build '%s'", buildID)
	}

	if len(buildKeys) == 0 {
		return nil, errors.Errorf("no keys found for build '%s", buildID)
	}

	buildChunks, testChunks, err := parseLogChunks(buildKeys)
	if err != nil {
		return nil, errors.Wrapf(err, "parsing log chunks from keys for build '%s'", buildID)
	}
	testChunks = filterLogChunksByTestID(testChunks, testID)

	testIDs, err := parseTestIDs(buildKeys)
	if err != nil {
		return nil, errors.Wrapf(err, "parsing test IDs from keys for build '%s'", buildID)
	}
	tr, err := testExecutionWindow(testIDs, testID)
	if err != nil {
		return nil, errors.Wrapf(err, "getting execution window for test '%s'", testID)
	}

	// Tests should never be filtered by a time range other than AllTime
	// since we always want to capture all the lines of either a single
	// test or all tests.
	return NewMergingIterator(NewBatchedLogIterator(testChunks, 4, AllTime), NewBatchedLogIterator(buildChunks, 4, tr)).Stream(ctx), nil
}

// LogChunk is a grouping of lines.
type LogChunk []LogLineItem

// groupLines breaks up a slice of LogLineItems into chunks. The sum of the sizes of lines' Data in each chunk is
// less than or equal to maxSize.
func groupLines(lines []LogLineItem, maxSize int) ([]LogChunk, error) {
	var chunks []LogChunk
	var currentChunk LogChunk

	logChars := 0
	for _, line := range lines {
		if len(line.Data) > maxSize {
			return nil, errors.Errorf("Log line exceeded %d bytes", maxSize)
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

// InsertLogLines uploads log lines for a given build or test to the
// pail-backed offline storage. If the test ID is not empty, the logs are
// appended to the test for the given build, otherwise the logs are appended to
// the top-level build. A build ID is required in both cases.
func InsertLogLines(ctx context.Context, buildID string, testID string, lines []LogLineItem, maxSize int) error {
	if len(lines) == 0 {
		return nil
	}

	chunks, err := groupLines(lines, maxSize)
	if err != nil {
		return errors.Wrapf(err, "grouping lines for build '%s' test '%s'", buildID, testID)
	}

	for _, chunk := range chunks {
		logChunkInfo := LogChunkInfo{}
		if err := logChunkInfo.fromLogChunk(buildID, testID, chunk); err != nil {
			return errors.Wrap(err, "parsing log chunk info")
		}

		var buffer bytes.Buffer
		numLines := 0
		for _, line := range chunk {
			// We are sometimes passed in a single log line that is
			// actually multiple lines, so we parse it into
			// separate lines and keep track of the count to make
			// sure we know the current number of lines.
			for _, parsedLine := range makeLogLineStrings(line) {
				buffer.WriteString(parsedLine)
				numLines += 1
			}
		}
		logChunkInfo.NumLines = numLines

		if err := env.Bucket().Put(ctx, logChunkInfo.key(), &buffer); err != nil {
			return errors.Wrap(err, "uploading log chunk")
		}
	}

	return nil
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

// parseLogChunks parses build and test log chunks from the buildKeys that correspond to log chunks
// and sorts them by start time.
func parseLogChunks(buildKeys []string) ([]LogChunkInfo, []LogChunkInfo, error) {
	var buildChunks, testChunks []LogChunkInfo
	for _, key := range buildKeys {
		if strings.HasSuffix(key, metadataFilename) {
			continue
		}

		var info LogChunkInfo
		if err := info.fromKey(key); err != nil {
			return nil, nil, errors.Wrap(err, "getting log chunk info from key name")
		}
		if info.TestID != "" {
			testChunks = append(testChunks, info)
		} else {
			buildChunks = append(buildChunks, info)
		}
	}

	sortLogChunksByStartTime(buildChunks)
	sortLogChunksByStartTime(testChunks)

	return buildChunks, testChunks, nil
}

// filterLogChunksByTestID returns the resulting slice of log chunks after
// filtering for chunks with the given test ID.
func filterLogChunksByTestID(chunks []LogChunkInfo, testID string) []LogChunkInfo {
	if testID == "" {
		return chunks
	}

	var filteredChunks []LogChunkInfo
	for _, chunk := range chunks {
		if chunk.TestID == testID {
			filteredChunks = append(filteredChunks, chunk)
		}
	}
	return filteredChunks
}

func sortLogChunksByStartTime(chunks []LogChunkInfo) {
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].Start.Before(chunks[j].Start)
	})
}

func makeLogLineStrings(logLine LogLineItem) []string {
	singleLines := strings.Split(logLine.Data, "\n")
	logLines := make([]string, 0, len(singleLines))
	for _, line := range singleLines {
		logLines = append(logLines, fmt.Sprintf("  0%20d%s\n", utility.UnixMilli(logLine.Timestamp), line))
	}
	return logLines
}
