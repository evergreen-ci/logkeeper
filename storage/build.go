package storage

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
)

const metadataFilename = "metadata.json"

// Build contains metadata about a build.
type Build struct {
	ID       string `json:"id"`
	Builder  string `json:"builder"`
	BuildNum int    `json:"buildnum"`
	TaskID   string `json:"task_id"`
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
		return "", errors.Wrap(err, "generating json to hash for build key")
	}

	if _, err := hasher.Write(hashstring); err != nil {
		return "", errors.Wrap(err, "hashing json for build key")
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (b *Build) key() string {
	return metadataKeyForBuild(b.ID)
}

func metadataKeyForBuild(id string) string {
	return fmt.Sprintf("%s%s", buildPrefix(id), metadataFilename)
}

func (b *Build) toJSON() ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling build metadata")
	}

	return data, nil
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("builds/%s/", buildID)
}
