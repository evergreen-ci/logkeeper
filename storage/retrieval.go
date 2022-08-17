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

// FindBuildByID returns the build metadata for the given ID from the offline
// blob storage bucket.
func (b *Bucket) FindBuildByID(ctx context.Context, id string) (*model.Build, error) {
	key := metadataKeyForBuild(id)
	reader, err := b.Get(ctx, key)
	if err != nil {
		return nil, errors.Wrapf(err, "getting build metadata for build '%s'", id)
	}

	var metadata buildMetadata
	if err = json.NewDecoder(reader).Decode(&metadata); err != nil {
		return nil, errors.Wrapf(err, "parsing build metadata for build '%s'", id)
	}
	build := metadata.toBuild()

	return &build, nil
}

// FindTestByID returns the test metadata for the given build ID and test ID
// from the offline blob storage bucket.
func (b *Bucket) FindTestByID(ctx context.Context, buildID string, testID string) (*model.Test, error) {
	key := metadataKeyForTest(buildID, testID)
	reader, err := b.Get(ctx, key)
	if err != nil {
		return nil, errors.Wrapf(err, "getting test metadata for build '%s' and test '%s'", buildID, testID)
	}

	var metadata testMetadata
	if err = json.NewDecoder(reader).Decode(&metadata); err != nil {
		return nil, errors.Wrapf(err, "parsing test metadata for build '%s' and test '%s'", buildID, testID)
	}
	test := metadata.toTest()

	return &test, nil
}

// FindTestsForBuild returns all of the test metadata for the given build ID
// from the offline blob storage bucket.
func (b *Bucket) FindTestsForBuild(ctx context.Context, buildID string) ([]model.Test, error) {
	iterator, err := b.List(ctx, buildTestsPrefix(buildID))
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
	tests := make([]model.Test, len(testIDs))
	for i, id := range testIDs {
		wg.Add(1)
		go func(testID string, idx int) {
			defer recovery.LogStackTraceAndContinue("finding test metadata for build from bucket")
			defer wg.Done()

			test, err := b.FindTestByID(ctx, buildID, testID)
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

// DownloadLogLines returns log lines for a given build ID and test ID. If the
// test ID is empty, this will return all logs lines in the build.
func (b *Bucket) DownloadLogLines(ctx context.Context, buildID string, testID string) (chan *model.LogLineItem, error) {
	buildChunks, testChunks, err := b.getLogChunks(ctx, buildID)
	if err != nil {
		return nil, errors.Wrapf(err, "getting log chunks for build '%s'", buildID)
	}
	testChunks, tr := filterLogChunksByTestID(testChunks, testID)

	return NewMergingIterator(NewBatchedLogIterator(b, testChunks, 4, tr), NewBatchedLogIterator(b, buildChunks, 4, tr)).Stream(ctx), nil
}

// getLogChunks returns the build and test log chunks for the given build ID
// sorted by start time.
func (b *Bucket) getLogChunks(ctx context.Context, buildID string) ([]LogChunkInfo, []LogChunkInfo, error) {
	iter, err := b.List(ctx, buildPrefix(buildID))
	if err != nil {
		return nil, nil, errors.Wrap(err, "listing chunks")
	}

	var buildChunks, testChunks []LogChunkInfo
	for iter.Next(ctx) {
		if strings.HasSuffix(iter.Item().Name(), metadataFilename) {
			continue
		}

		var info LogChunkInfo
		if err := info.fromKey(iter.Item().Name()); err != nil {
			return nil, nil, errors.Wrap(err, "getting log chunk info from key name")
		}
		if info.TestID != "" {
			testChunks = append(testChunks, info)
		} else {
			buildChunks = append(buildChunks, info)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, nil, errors.Wrap(err, "getting log chunks")
	}

	sortLogChunksByStartTime(buildChunks)
	sortLogChunksByStartTime(testChunks)

	return buildChunks, testChunks, nil
}

// filterLogChunksByTestID returns (1) the resulting slice of log chunks after
// filtering for chunks with the given test ID and (2) the appropriate test
// execution time range for the given test ID.
func filterLogChunksByTestID(chunks []LogChunkInfo, testID string) ([]LogChunkInfo, TimeRange) {
	if testID == "" {
		return chunks, NewTimeRange(TimeRangeMin, TimeRangeMax)
	}

	var (
		testStart, testEnd time.Time
		filteredChunks     []LogChunkInfo
	)
	for i := range chunks {
		if chunks[i].TestID != testID {
			continue
		}

		if chunks[i].TestID == testID {
			if testStart.IsZero() {
				testStart = chunks[i].Start
			}
			if chunks[i].End.After(testEnd) {
				testEnd = chunks[i].End
			}
		}
		filteredChunks = append(filteredChunks, chunks[i])
	}
	if testStart.IsZero() {
		// If the testStart variable is zero, this means that we never
		// found a test chunk matching the given test ID and we should
		// return an empty slice and time range.
		return nil, TimeRange{}
	}

	// We need to iterate through the original chunk slice to find the
	// first chunk of the next test following given test ID. The start time
	// of that chunk will serve as the end of the time range.
	tr := TimeRange{StartAt: testStart}
	for _, chunk := range chunks {
		if chunk.Start.After(testEnd) {
			tr.EndAt = chunk.Start
			break
		}
	}
	if tr.EndAt.IsZero() {
		// If the end of the time range is not set, this means that the
		// given test ID is the last test of the build. We can safely
		// set the end of the time range to an "infinitely" far time
		// in the future.
		tr.EndAt = TimeRangeMax
	}

	return filteredChunks, tr
}

func sortLogChunksByStartTime(chunks []LogChunkInfo) {
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].Start.Before(chunks[j].Start)
	})
}
