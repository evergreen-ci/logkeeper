package storage

import (
	"bytes"
	"context"

	"github.com/pkg/errors"
)

// UploadBuildMetadata uploads metadata for a new build to the pail-backed
// offline storage.
func (b *Bucket) UploadBuildMetadata(ctx context.Context, build Build) error {
	data, err := build.toJSON()
	if err != nil {
		return err
	}

	return errors.Wrapf(b.Put(ctx, build.key(), bytes.NewReader(data)), "uploading metadata for build '%s'", build.ID)
}

// UploadTestMetadata uploads metadata for a new test to the pail-backed
// offline storage.
func (b *Bucket) UploadTestMetadata(ctx context.Context, test Test) error {
	data, err := test.toJSON()
	if err != nil {
		return nil
	}

	return errors.Wrapf(b.Put(ctx, test.key(), bytes.NewReader(data)), "uploading metadata for test '%s'", test.ID)
}

// InsertLogChunks uploads a new chunk of logs for a given build or test to the
// pail-backed offline storage. If the test ID is not empty, the logs are
// appended to the test for the given build, otherwise the logs are appended to
// the top-level build. A build ID is required in both cases.
func (b *Bucket) InsertLogChunks(ctx context.Context, buildID string, testID string, chunks []LogChunk) error {
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}

		logChunkInfo := LogChunkInfo{}
		err := logChunkInfo.fromLogChunk(buildID, testID, chunk)
		if err != nil {
			return errors.Wrap(err, "parsing log chunks")
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

		if err := b.Put(ctx, logChunkInfo.key(), &buffer); err != nil {
			return errors.Wrap(err, "uploading log chunk")
		}
	}

	return nil
}
