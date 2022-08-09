package storage

import (
	"bytes"
	"context"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/pkg/errors"
)

func (b *Bucket) UploadBuildMetadata(ctx context.Context, build model.Build) error {
	metadata := newBuildMetadata(build)
	json, err := metadata.toJSON()
	if err != nil {
		return errors.Wrap(err, "getting metadata JSON for build")
	}

	return errors.Wrapf(b.Put(ctx, metadata.key(), bytes.NewReader(json)), "putting metadata for build '%s'", build.Id)
}

func (b *Bucket) UploadTestMetadata(ctx context.Context, test model.Test) error {
	metadata := newTestMetadata(test)
	json, err := metadata.toJSON()
	if err != nil {
		return errors.Wrap(err, "getting metadata JSON for test")
	}

	return errors.Wrapf(b.Put(ctx, metadata.key(), bytes.NewReader(json)), "putting metadata for test '%s'", test.Id)
}

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
		for _, line := range chunk {
			buffer.WriteString(makeLogLineString(line))
		}

		if err := b.Put(ctx, logChunkInfo.key(), &buffer); err != nil {
			return errors.Wrap(err, "uploading log entry to bucket")
		}
	}

	return nil
}
