package storage

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/pail"
)

type Storage struct {
	bucket pail.Bucket
}

func NewStorage(bucket pail.Bucket) Storage {
	return Storage{
		bucket: bucket,
	}
}

func parseName(name string) (start time.Time, end time.Time, numLines int64, err error) {
	nameParts := strings.Split(name, "_")
	startNanos, err := strconv.ParseInt(nameParts[0], 10, 64)
	if err != nil {
		return
	}
	start = time.Unix(0, startNanos)

	endNanos, err := strconv.ParseInt(nameParts[1], 10, 64)
	if err != nil {
		return
	}
	end = time.Unix(0, endNanos)

	numLines, err = strconv.ParseInt(nameParts[2], 10, 64)
	if err != nil {
		return
	}
	return
}

func (storage *Storage) getAllChunks(context context.Context, buildId string) ([]LogChunkInfo, error) {
	buildPrefix := fmt.Sprintf("/%s/", buildId)

	iterator, listErr := storage.bucket.List(context, buildPrefix)
	buildChunks := []LogChunkInfo{}
	if listErr != nil {
		return nil, listErr
	}
	for iterator.Next(context) {
		if strings.HasSuffix(iterator.Item().Name(), "metadata.json") {
			continue
		}
		if strings.Contains(iterator.Item().Name(), "/tests/") {
			keyParts := strings.Split(iterator.Item().Name(), "/")
			buildID := keyParts[1]
			testID := keyParts[3]
			name := keyParts[4]
			start, end, numLines, nameErr := parseName(name)
			if nameErr != nil {
				return nil, nameErr
			}
			buildChunks = append(buildChunks, LogChunkInfo{
				BuildID:  buildID,
				TestID:   testID,
				Start:    start,
				End:      end,
				NumLines: int(numLines),
			})
		} else {
			keyParts := strings.Split(iterator.Item().Name(), "/")
			buildID := keyParts[1]
			name := keyParts[2]
			start, end, numLines, nameErr := parseName(name)
			if nameErr != nil {
				return nil, nameErr
			}
			buildChunks = append(buildChunks, LogChunkInfo{
				BuildID:  buildID,
				TestID:   "",
				Start:    start,
				End:      end,
				NumLines: int(numLines),
			})
		}
	}
	return buildChunks, nil
}

func (storage *Storage) getBuildAndTestChunks(context context.Context, buildId string) ([]LogChunkInfo, []LogChunkInfo, error) {
	chunks, err := storage.getAllChunks(context, buildId)
	if err != nil {
		return nil, nil, err
	}

	buildChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		if chunks[i].TestID == "" {
			buildChunks = append(buildChunks, chunks[i])
		}
	}

	testChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Find our test id
		if chunks[i].TestID != "" {
			testChunks = append(testChunks, chunks[i])
		}
	}
	return buildChunks, testChunks, nil
}

func getLatestTime(chunks []LogChunkInfo) time.Time {
	var latestTime = chunks[len(chunks)-1].End
	for _, chunk := range chunks {
		if chunk.End.After(latestTime) {
			latestTime = chunk.End
		}
	}
	return latestTime
}

func sortByStartTime(chunks []LogChunkInfo) {
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].Start.Before(chunks[j].Start)
	})
}

func minTime(first time.Time, second time.Time) time.Time {
	if first.Before(second) {
		return first
	} else {
		return second
	}
}

func maxTime(first time.Time, second time.Time) time.Time {
	if first.Before(second) {
		return second
	} else {
		return first
	}
}

func (storage *Storage) GetAllLogLines(context context.Context, buildId string) (LogIterator, error) {
	buildChunks, testChunks, err := storage.getBuildAndTestChunks(context, buildId)
	if err != nil {
		return nil, err
	}

	sortByStartTime(buildChunks)
	sortByStartTime(testChunks)

	timeRange := TimeRange{
		StartAt: minTime(buildChunks[0].Start, testChunks[0].Start),
		EndAt:   maxTime(getLatestTime(buildChunks), getLatestTime(testChunks)),
	}

	buildChunkIterator := NewBatchedLogIterator(storage.bucket, buildChunks, 4, timeRange)
	testChunkIterator := NewBatchedLogIterator(storage.bucket, testChunks, 4, timeRange)

	// Merge test and build logs
	return NewMergingIterator(testChunkIterator, buildChunkIterator), nil
}

func testChunksWithId(chunks []LogChunkInfo, testID string) []LogChunkInfo {
	testChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Find our test id
		if chunks[i].TestID == testID {
			testChunks = append(testChunks, chunks[i])
		}
	}
	return testChunks
}

// Gets the first chunk in the list with a start time after "target", otherwise falls back to the
// fallback time if there are no such chunks.
func getFirstTestChunkAfter(allTestChunks []LogChunkInfo, target time.Time, fallback time.Time) time.Time {
	for _, chunk := range allTestChunks {
		if chunk.Start.After(target) {
			return chunk.Start
		}
	}
	return fallback
}

func (storage *Storage) GetTestLogLines(context context.Context, buildId string, testId string) (LogIterator, error) {
	buildChunks, allTestChunks, err := storage.getBuildAndTestChunks(context, buildId)
	if err != nil {
		return nil, err
	}

	sortByStartTime(allTestChunks)

	testChunks := testChunksWithId(allTestChunks, testId)

	sortByStartTime(testChunks)

	// We want to get all logs up to the next test chunk after the chunks in our queried test.
	// If there are no test chunks after our queried test, then this should set the end time
	// to the latest end time of all the chunks we have.
	lastTestChunkEnd := getLatestTime(testChunks)
	maxPossibleTime := maxTime(getLatestTime(buildChunks), lastTestChunkEnd)
	logEndTime := getFirstTestChunkAfter(allTestChunks, lastTestChunkEnd, maxPossibleTime)

	testTimeRange := TimeRange{
		StartAt: testChunks[0].Start,
		EndAt:   logEndTime,
	}

	testChunkIterator := NewBatchedLogIterator(storage.bucket, testChunks, 4, testTimeRange)

	sortByStartTime(buildChunks)
	// This batchedlogiterator will filter out buildChunks that don't intestect with testTimeRange
	buildChunkIterator := NewBatchedLogIterator(storage.bucket, buildChunks, 4, testTimeRange)

	// Merge everything together
	return NewMergingIterator(testChunkIterator, buildChunkIterator), nil
}
