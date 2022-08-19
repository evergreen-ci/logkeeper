package storage

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

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
	buildKeys, err := b.getBuildKeys(ctx, buildID)
	if err != nil {
		return nil, errors.Wrapf(err, "getting keys for build '%s'", buildID)
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
	tr := testExecutionWindow(testIDs, testID)

	return NewMergingIterator(NewBatchedLogIterator(b, testChunks, 4, tr), NewBatchedLogIterator(b, buildChunks, 4, tr)).Stream(ctx), nil
}

// getBuildKeys returns the all the keys contained within the build prefix.
func (b *Bucket) getBuildKeys(ctx context.Context, buildID string) ([]string, error) {
	iter, err := b.List(ctx, buildPrefix(buildID))
	if err != nil {
		return nil, errors.Wrapf(err, "listing keys for build '%s'", buildID)
	}

	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Item().Name())
	}

	return keys, nil
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

// parseTestIDs parses test IDs from the buildKeys that correspond to test metadata files
// and sorts them by creation time.
func parseTestIDs(buildKeys []string) ([]model.TestID, error) {
	var testIDs []model.TestID
	for _, key := range buildKeys {
		if !strings.HasSuffix(key, metadataFilename) {
			continue
		}
		if !strings.Contains(key, "/tests/") {
			continue
		}
		testID, err := testIDFromKey(key)
		if err != nil {
			return nil, errors.Wrap(err, "getting test ID from metadata key")
		}
		testIDs = append(testIDs, model.TestID(testID))
	}

	sort.Slice(testIDs, func(i, j int) bool {
		return testIDs[i].Timestamp().Before(testIDs[j].Timestamp())
	})

	return testIDs, nil
}

// testExecutionWindow returns the TimeRange from the creation of this test to the creation
// of the next test. If the testID isn't found the returned TimeRange is unbounded.
// If there is no later test then the end time is TimeRangeMax.
func testExecutionWindow(allTestIDs []model.TestID, testID string) TimeRange {
	tr := AllTime
	if testID == "" {
		return tr
	}

	var found bool
	var testIndex int
	for i, id := range allTestIDs {
		if string(id) == testID {
			found = true
			testIndex = i
		}
	}
	if !found {
		return tr
	}

	tr.StartAt = allTestIDs[testIndex].Timestamp()

	if testIndex < len(allTestIDs)-1 {
		tr.EndAt = allTestIDs[testIndex+1].Timestamp()
	}

	return tr
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
