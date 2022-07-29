package storage

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/logkeeper/models"
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

func (storage *Storage) getAllChunks(context context.Context, buildId string) ([]models.LogChunkInfo, error) {
	buildPrefix := fmt.Sprintf("/%s/", buildId)

	iterator, listErr := storage.bucket.List(context, buildPrefix)
	buildChunks := []models.LogChunkInfo{}
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
			buildChunks = append(buildChunks, models.LogChunkInfo{
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
			buildChunks = append(buildChunks, models.LogChunkInfo{
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

func (storage *Storage) GetTestLogLines(context context.Context, buildId string, testId string) (LogIterator, error) {
	chunks, err := storage.getAllChunks(context, buildId)
	if err != nil {
		return nil, err
	}

	testChunks := []models.LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Find our test id
		if chunks[i].TestID == testId {
			testChunks = append(testChunks, chunks[i])
		}
	}
	sort.Slice(testChunks, func(i, j int) bool {
		return testChunks[i].Start.Before(testChunks[j].Start)
	})

	testTimeRange := models.TimeRange{
		StartAt: testChunks[0].Start,
		EndAt:   testChunks[len(testChunks)-1].End,
	}

	testChunkIterator := NewBatchedLogIterator(storage.bucket, testChunks, 4, testTimeRange)

	buildChunks := []models.LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Include any build logs that are in the time range of our test
		chunkTimeRange := models.TimeRange{
			StartAt: chunks[i].Start,
			EndAt:   chunks[i].End,
		}
		// check if the global build chunk's time range intersects the test's time range, and if so
		// add it to our list of build chunks, but constrained to the test's time range to only
		// include entries during that time.
		if chunks[i].TestID == "" && testTimeRange.Intersects(chunkTimeRange) {
			buildChunks = append(buildChunks, chunks[i])
		}
	}

	sort.Slice(buildChunks, func(i, j int) bool {
		return buildChunks[i].Start.Before(buildChunks[j].Start)
	})
	buildChunkIterator := NewBatchedLogIterator(storage.bucket, buildChunks, 4, testTimeRange)

	// Merge everything together
	return NewMergingIterator(testChunkIterator, buildChunkIterator), nil
}
