package model

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	// DeletePassedTestCutoff is the TTL for passed tests.
	DeletePassedTestCutoff = 30 * (24 * time.Hour)
	// BuildsCollection is the name of the builds collection in the database.
	BuildsCollection = "builds"
)

// Build contains metadata about a build.
type Build struct {
	Id       string    `bson:"_id"`
	Builder  string    `bson:"builder"`
	BuildNum int       `bson:"buildnum"`
	Started  time.Time `bson:"started"`
	Name     string    `bson:"name"`
	Info     BuildInfo `bson:"info"`
	Failed   bool      `bson:"failed"`
	Phases   []string  `bson:"phases"`
	Seq      int       `bson:"seq"`
	S3       bool      `bson:"s3,omitempty"`
}

// BuildInfo contains additional metadata about a build.
type BuildInfo struct {
	// TaskID is the ID of the task in Evergreen that generated this build.
	TaskID string `bson:"task_id" json:"task_id"`
}

// Insert inserts the build into the builds collection.
func (b *Build) Insert() error {
	db, closeSession := db.DB()
	defer closeSession()

	return db.C(BuildsCollection).Insert(b)
}

// FindBuildById returns the build with the given id.
func FindBuildById(id string) (*Build, error) {
	db, closeSession := db.DB()
	defer closeSession()

	build := &Build{}
	err := db.C(BuildsCollection).Find(bson.M{"_id": id}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

// FindBuildByBuilder returns the build corresponding to the builder and buildnum.
func FindBuildByBuilder(builder string, buildnum int) (*Build, error) {
	db, closeSession := db.DB()
	defer closeSession()

	build := &Build{}
	err := db.C(BuildsCollection).Find(bson.M{"builder": builder, "buildnum": buildnum}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

// UpdateFailedBuild sets the failed field for the build with the given id.
func UpdateFailedBuild(id string) error {
	db, closeSession := db.DB()
	defer closeSession()

	err := db.C(BuildsCollection).UpdateId(id, bson.M{"$set": bson.M{"failed": true}})
	return errors.Wrapf(err, "problem setting failed state on build %v", id)
}

// IncrementSequence increments the build's sequence number by the given count.
func (b *Build) IncrementSequence(count int) error {
	db, closeSession := db.DB()
	defer closeSession()

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": count}}, ReturnNew: true}
	_, err := db.C("builds").Find(bson.M{"_id": b.Id}).Apply(change, b)
	return errors.Wrapf(err, "incrementing sequence number for build '%s'", b.Id)
}

// StreamingGetOldBuilds returns a channel containing builds that are ready to be deleted
// and a channel for any errors encountered.
// The channels are closed when all the matching builds have been returned or we encounter an error.
func StreamingGetOldBuilds(ctx context.Context) (<-chan Build, <-chan error) {
	db, closeSession := db.DB()

	errOut := make(chan error)
	out := make(chan Build)
	db.Session.SetSocketTimeout(5 * time.Minute)
	go func() {
		defer closeSession()
		defer close(errOut)
		defer close(out)
		defer recovery.LogStackTraceAndContinue("streaming query")

		iter := db.C(BuildsCollection).Find(bson.M{
			"started": bson.M{"$lte": time.Now().Add(-DeletePassedTestCutoff)},
			"$or": []bson.M{
				{"failed": bson.M{"$exists": false}},
				{"failed": bson.M{"$eq": false}},
			},
			"$and": []bson.M{
				{"info.task_id": bson.M{"$exists": true}},
				{"info.task_id": bson.M{"$ne": ""}},
			},
		}).Iter()
		build := Build{}
		for iter.Next(&build) {
			out <- build
			build = Build{}

			if ctx.Err() != nil {
				return
			}
		}

		if err := iter.Err(); err != nil {
			errOut <- err
			return
		}
	}()

	return out, errOut
}

// RemoveBuild removes the build with the given ID from the database.
func RemoveBuild(buildID string) error {
	db, closeSession := db.DB()
	defer closeSession()

	return errors.Wrap(db.C(BuildsCollection).RemoveId(buildID), "deleting build record")
}

const (
	builderFieldNum  = 1
	buildNumFieldNum = 2
)

func makeBinaryRepresentation(builder string, buildNum int) []byte {
	var buf bytes.Buffer
	builderBytes := []byte(builder)
	buildNumBytes := []byte(strconv.Itoa(buildNum))
	binary.Write(&buf, binary.BigEndian, uint32(builderFieldNum))
	binary.Write(&buf, binary.BigEndian, uint32(len(builderBytes)))
	buf.Write(builderBytes)
	binary.Write(&buf, binary.BigEndian, uint32(buildNumFieldNum))
	binary.Write(&buf, binary.BigEndian, uint32(len(buildNumBytes)))
	buf.Write(buildNumBytes)
	return buf.Bytes()
}

// Generates a new build ID based on the hash of builder and buildNum
func NewBuildId(builder string, buildNum int) (string, error) {
	hasher := md5.New()

	if _, err := hasher.Write(makeBinaryRepresentation(builder, buildNum)); err != nil {
		return "", errors.Wrap(err, "hashing json for build key")
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
