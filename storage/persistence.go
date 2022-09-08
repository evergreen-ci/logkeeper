package storage

import (
	"bytes"
	"context"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/pkg/errors"
)

// UploadBuildMetadata uploads metadata for a new build to the offline blob
// bucket storage.
func (b *Bucket) UploadBuildMetadata(ctx context.Context, build model.Build) error {
	metadata := newBuildMetadata(build)
	json, err := metadata.toJSON()
	if err != nil {
		return errors.Wrap(err, "getting metadata JSON for build")
	}

	return errors.Wrapf(b.Put(ctx, metadata.key(), bytes.NewReader(json)), "putting metadata for build '%s'", build.Id)
}

// UploadBuildMetadata uploads metadata for a new test to the offline blob
// bucket storage.
func (b *Bucket) UploadTestMetadata(ctx context.Context, test model.Test) error {
	metadata := newTestMetadata(test)
	json, err := metadata.toJSON()
	if err != nil {
		return errors.Wrap(err, "getting metadata JSON for test")
	}

	return errors.Wrapf(b.Put(ctx, metadata.key(), bytes.NewReader(json)), "putting metadata for test '%s'", test.Id)
}

// InsertLogChunks uploads a new chunk of logs for a given build or test to the
// offline blob bucket storage. If a test ID is not empty, the logs are
// appended to the test for the given build, otherwise the logs are appended to
// the top-level build. A build ID is required in both cases.
func (b *Bucket) InsertLogChunks(ctx context.Context, buildID string, testID string, chunks []model.LogChunk) error {
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
			return errors.Wrap(err, "uploading log entry to bucket")
		}
	}

	return nil
}
