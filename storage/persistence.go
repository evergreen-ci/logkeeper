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
		return errors.Wrap(err, "getting metadata JSON")
	}

	return errors.Wrapf(b.Put(ctx, metadata.key(), bytes.NewReader(json)), "putting build metadata for build '%s'", build.Id)
}
