package model

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/pail"
	"github.com/pkg/errors"
)

const metadataFilename = "metadata.json"

// Build contains metadata about a build.
type Build struct {
	ID            string `json:"id"`
	Builder       string `json:"builder"`
	BuildNum      int    `json:"buildnum"`
	TaskID        string `json:"task_id"`
	TaskExecution *int   `json:"execution"`
}

// UploadMetadata uploads metadata for a new build to the pail-backed
// offline storage.
func (b *Build) UploadMetadata(ctx context.Context) error {
	data, err := b.toJSON()
	if err != nil {
		return err
	}

	return errors.Wrapf(env.Bucket().Put(ctx, b.key(), bytes.NewReader(data)), "uploading metadata for build '%s'", b.ID)
}

func (b *Build) key() string {
	return metadataKeyForBuild(b.ID)
}

func (b *Build) toJSON() ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling build metadata")
	}

	return data, nil
}

func metadataKeyForBuild(id string) string {
	return fmt.Sprintf("%s%s", buildPrefix(id), metadataFilename)
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("builds/%s/", buildID)
}

// NewBuildID generates a new build ID based on the hash of the given builder
// and build number.
func NewBuildID(builder string, buildNum int) (string, error) {
	hasher := md5.New()

	// This depends on the fact that Go's JSON implementation sorts JSON
	// keys lexicographically for maps, which ensures consistent encoding.
	jsonMap := make(map[string]interface{})
	jsonMap["builder"] = builder
	jsonMap["buildNum"] = buildNum
	hashstring, err := json.Marshal(jsonMap)
	if err != nil {
		return "", errors.Wrap(err, "marshalling build ID data to JSON")
	}

	if _, err := hasher.Write(hashstring); err != nil {
		return "", errors.Wrap(err, "writing the hash for the build ID")
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// FindBuildByID returns the build metadata for the given ID from the pail-backed
// offline storage.
func FindBuildByID(ctx context.Context, id string) (*Build, error) {
	reader, err := env.Bucket().Get(ctx, metadataKeyForBuild(id))
	if pail.IsKeyNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "getting build metadata for build '%s'", id)
	}

	build := &Build{}
	if err = json.NewDecoder(reader).Decode(build); err != nil {
		return nil, errors.Wrapf(err, "parsing build metadata for build '%s'", id)
	}

	return build, nil
}

// CheckBuildMetadata returns whether the metadata file exists for the given build.
func CheckBuildMetadata(ctx context.Context, id string) (bool, error) {
	return checkMetadata(ctx, id, "")
}

// checkMetadata returns whether the metadata file exists for the given build
// or test. If the test ID is not empty, the metadata of the test for the given
// build is checked, otherwise the top-level build metadata is checked. A build
// ID is required in both cases.
func checkMetadata(ctx context.Context, buildID string, testID string) (bool, error) {
	var key string
	if testID == "" {
		key = metadataKeyForBuild(buildID)
	} else {
		key = metadataKeyForTest(buildID, testID)
	}

	exists, err := env.Bucket().Exists(ctx, key)
	if err != nil {
		return false, errors.Wrap(err, "checking if metadata file exists")
	}

	return exists, nil
}

// getBuildKeys returns the all the keys contained within the build prefix.
func getBuildKeys(ctx context.Context, buildID string) ([]string, error) {
	iter, err := env.Bucket().List(ctx, buildPrefix(buildID))
	if err != nil {
		return nil, errors.Wrapf(err, "listing keys for build '%s'", buildID)
	}

	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Item().Name())
	}

	if err := iter.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating build keys")
	}

	return keys, nil
}
