package storage

import (
	"context"
	"sort"
	"strings"
)

func (b *Bucket) getAllChunks(context context.Context, buildId string) ([]LogChunkInfo, error) {
	iterator, listErr := b.List(context, buildPrefix(buildId))
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

func (b *Bucket) GetTestLogLines(context context.Context, buildId string, testId string) (LogIterator, error) {
	chunks, err := b.getAllChunks(context, buildId)
	if err != nil {
		return nil, err
	}

	testChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Find our test id
		if chunks[i].TestID == testId {
			testChunks = append(testChunks, chunks[i])
		}
	}
	sort.Slice(testChunks, func(i, j int) bool {
		return testChunks[i].Start.Before(testChunks[j].Start)
	})

	var latestTime = testChunks[len(testChunks)-1].End
	for _, chunk := range testChunks {
		if chunk.End.After(latestTime) {
			latestTime = chunk.End
		}
	}

	testTimeRange := TimeRange{
		StartAt: testChunks[0].Start,
		EndAt:   testChunks[len(testChunks)-1].End,
	}

	testChunkIterator := NewBatchedLogIterator(b, testChunks, 4, testTimeRange)

	buildChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Include any build logs that are in the time range of our test
		chunkTimeRange := TimeRange{
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
	buildChunkIterator := NewBatchedLogIterator(b, buildChunks, 4, testTimeRange)

	// Merge everything together
	return NewMergingIterator(testChunkIterator, buildChunkIterator), nil
}
