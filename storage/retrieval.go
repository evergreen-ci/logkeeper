package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
)

func (b *Bucket) getAllChunks(context context.Context, buildId string) ([]LogChunkInfo, error) {
	iterator, listErr := b.List(context, buildPrefix(buildId))
	buildChunks := []LogChunkInfo{}
	if listErr != nil {
		return nil, listErr
	}
	for iterator.Next(context) {
		if strings.HasSuffix(iterator.Item().Name(), metadataFilename) {
			continue
		}
		var info LogChunkInfo
		if err := info.fromKey(iterator.Item().Name()); err != nil {
			return nil, errors.Wrap(err, "getting log chunk info from key name")
		}
		buildChunks = append(buildChunks, info)
	}
	return buildChunks, nil
}

func (storage *Bucket) getBuildAndTestChunks(context context.Context, buildId string) ([]LogChunkInfo, []LogChunkInfo, error) {
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

func (storage *Bucket) GetAllLogLines(context context.Context, buildId string) (LogIterator, error) {
	buildChunks, testChunks, err := storage.getBuildAndTestChunks(context, buildId)
	if err != nil {
		return nil, err
	}

	sortByStartTime(buildChunks)
	sortByStartTime(testChunks)

	timeRange := NewTimeRange(TimeRangeMin, TimeRangeMax)

	buildChunkIterator := NewBatchedLogIterator(storage, buildChunks, 4, timeRange)
	testChunkIterator := NewBatchedLogIterator(storage, testChunks, 4, timeRange)

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

// Gets the first chunk in the list with a start time after "target", otherwise falls back to
// TimeRangeMax.
func getFirstTestChunkAfter(allTestChunks []LogChunkInfo, target time.Time) time.Time {
	for _, chunk := range allTestChunks {
		if chunk.Start.After(target) {
			return chunk.Start
		}
	}
	return TimeRangeMax
}

func (storage *Bucket) GetTestLogLines(context context.Context, buildId string, testId string) (LogIterator, error) {
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
	logEndTime := getFirstTestChunkAfter(allTestChunks, lastTestChunkEnd)

	testTimeRange := NewTimeRange(testChunks[0].Start, logEndTime)

	testChunkIterator := NewBatchedLogIterator(storage, testChunks, 4, testTimeRange)

	sortByStartTime(buildChunks)
	// Before fetching, this batchedlogiterator will filter out buildChunks that don't intersect with testTimeRange
	buildChunkIterator := NewBatchedLogIterator(storage, buildChunks, 4, testTimeRange)

	// Merge everything together
	return NewMergingIterator(testChunkIterator, buildChunkIterator), nil
}
