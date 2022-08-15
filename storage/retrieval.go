package storage

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/recovery"
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

func (storage *Bucket) GetAllLogLines(context context.Context, buildId string) (chan *model.LogLineItem, error) {
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
	return NewMergingIterator(testChunkIterator, buildChunkIterator).Channel(context), nil
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

func (storage *Bucket) GetTestLogLines(context context.Context, buildId string, testId string) (chan *model.LogLineItem, error) {
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
	return NewMergingIterator(testChunkIterator, buildChunkIterator).Channel(context), nil
}

func (b *Bucket) FindBuildByID(ctx context.Context, id string) (*model.Build, error) {
	key := metadataKeyForBuildId(id)
	reader, err := b.Get(ctx, key)
	if err != nil {
		return nil, errors.Wrapf(err, "fetching build metadata for build '%s'", id)
	}

	metadata := buildMetadata{}
	decoder := json.NewDecoder(reader)
	err = decoder.Decode(&metadata)

	if err != nil {
		return nil, errors.Wrapf(err, "parsing build metadata for build '%s'", id)
	}

	build := metadata.toBuild()
	return &build, nil
}

func (b *Bucket) FindTestByID(ctx context.Context, buildId string, testId string) (*model.Test, error) {
	key := metadataKeyForTest(buildId, testId)
	reader, err := b.Get(ctx, key)
	if err != nil {
		return nil, errors.Wrapf(err, "fetching test metadata for build: '%s' and test: '%s'", buildId, testId)
	}

	metadata := testMetadata{}
	decoder := json.NewDecoder(reader)
	err = decoder.Decode(&metadata)
	if err != nil {
		return nil, errors.Wrapf(err, "parsing test metadata for build: '%s' and test: '%s'", buildId, testId)
	}

	test := metadata.toTest()
	return &test, nil
}

func (b *Bucket) FindTestsForBuild(ctx context.Context, buildId string) ([]model.Test, error) {
	iterator, listErr := b.List(ctx, buildTestsPrefix(buildId))
	testIds := []string{}
	if listErr != nil {
		return nil, errors.Wrapf(listErr, "listing test keys for build '%s'	", buildId)
	}
	for iterator.Next(ctx) {
		if strings.HasSuffix(iterator.Item().Name(), metadataFilename) {
			testId, parseError := testIdFromKey(iterator.Item().Name())
			if parseError != nil {
				return nil, errors.Wrapf(parseError, "parsing test metadata key for build '%s'", buildId)
			}
			testIds = append(testIds, testId)
		}
	}

	var wg sync.WaitGroup
	catcher := grip.NewBasicCatcher()

	testResults := make([]model.Test, len(testIds))

	for index, testId := range testIds {
		wg.Add(1)
		closureTestId := testId
		closureIndex := index
		go func() {
			defer recovery.LogStackTraceAndContinue("fetching tests from S3 for build")
			defer wg.Done()
			test, err := b.FindTestByID(ctx, buildId, closureTestId)
			if err != nil {
				catcher.Wrapf(err, "fetching test ID '%s' under build ID '%s'", closureTestId, buildId)
			} else {
				testResults[closureIndex] = *test
			}
		}()
	}

	wg.Wait()

	if catcher.HasErrors() {
		return nil, catcher.Resolve()
	}

	return testResults, nil
}
