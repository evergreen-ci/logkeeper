package storage

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/pkg/errors"
)

func (b *Bucket) UploadBuildMetadata(ctx context.Context, build model.Build) error {
	metadata := newBuildMetadata(build)
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return errors.Wrap(err, "marshaling metadata")
	}

	return errors.Wrapf(b.Put(ctx, metadata.key(), bytes.NewReader(metadataJSON)), "putting build metadata for build '%s'", build.Id)
}
