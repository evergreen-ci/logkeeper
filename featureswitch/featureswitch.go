package featureswitch

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/mongodb/grip"
)

const s3WriteFeatureSwitch = "LK_WRITE_S3_FEATURE_SWITCH"

func hashToFloat(hash []byte) float64 {
	// Use the first 4 bytes of the hash to construct a Uint32, since
	// that is the size of a uint32 and the hash is longer (16 bytes).
	value := binary.BigEndian.Uint32(hash[:4])
	// The plus one ensures that we return a number in the range 0 to 1,
	// inclusive of 0 and exclusive of 1. The math in the denominator is
	// untyped and should just be cast to a float64, so we shouldn't
	// have to worry about overflow.
	return float64(value) / (math.MaxUint32 + 1)
}

func getThreshold(featureSwitch string) float64 {
	value := os.Getenv(featureSwitch)
	floatValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		// If we don't have a parseable feature flag just log the error and return 0.
		// Our callers can't do anything about an unparseable feature flag.
		grip.Error(fmt.Sprintf("getting feature flag '%s'", featureSwitch))
		return 0
	} else {
		return floatValue
	}
}

func matchesFeatureForHash(featureSwitch string, hash []byte) bool {
	value := hashToFloat(hash)
	threshold := getThreshold(featureSwitch)
	// value is guaranteed to be in the range [0, 1), which means that
	// '<' will always return false for a threshold of 0 and always return
	// true for a threshold of 1.0.
	return value < threshold
}

func matchesFeatureForString(featureSwitch string, data string) bool {
	hasher := md5.New()
	// Write both the featureswitch name and data into our hasher.
	// This is  important so that we can use the same key with a
	// different featureswitch to get a different value from the hash.
	hasher.Write([]byte(featureSwitch))
	hasher.Write([]byte(data))
	hash := hasher.Sum(nil)
	return matchesFeatureForHash(featureSwitch, hash)
}

func setFeatureSwitchLevel(featureSwitch string, level float64) func() {
	oldValue, wasSet := os.LookupEnv(featureSwitch)
	os.Setenv(featureSwitch, fmt.Sprintf("%.3f", level))
	return func() {
		if !wasSet {
			os.Unsetenv(featureSwitch)
		} else {
			os.Setenv(featureSwitch, oldValue)
		}
	}
}

func SetWriteToS3Level(level float64) func() {
	return setFeatureSwitchLevel(s3WriteFeatureSwitch, level)
}

func WriteToS3Enabled(buildID string) bool {
	return matchesFeatureForString(s3WriteFeatureSwitch, buildID)
}
