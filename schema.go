package logkeeper

import (
	"fmt"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	// 	"regexp"
	"time"
)

const logKeeperDB = "logkeeper"

type Test struct {
	Id        bson.ObjectId          `bson:"_id"`
	BuildId   bson.ObjectId          `bson:"build_id"`
	BuildName string                 `bson:"build_name"`
	Name      string                 `bson:"name"`
	Command   string                 `bson:"command"`
	Started   time.Time              `bson:"started"`
	Ended     *time.Time             `bson:"ended"`
	Info      map[string]interface{} `bson:"info"`
	Failed    bool                   `bson:"failed"`
	Phase     string                 `bson:"phase"`
	Seq       int                    `bson:"seq",omitempty`
}

type LogKeeperBuild struct {
	Id       bson.ObjectId          `bson:"_id"`
	Builder  string                 `bson:"builder"`
	BuildNum int                    `bson:"buildnum"`
	Started  time.Time              `bson:"started"`
	Name     string                 `bson:"name"`
	Info     map[string]interface{} `bson:"info"`
	Phases   []string               `bson:"phases"`
	Seq      int                    `bson:"seq",omitempty`
}

func findTest(db *mgo.Database, id string) (*Test, error) {
	if !bson.IsObjectIdHex(id) {
		return nil, nil
	}
	test := &Test{}

	err := db.C("tests").Find(bson.M{"_id": bson.ObjectIdHex(id)}).One(test)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return test, nil
}

func findTestsForBuild(db *mgo.Database, buildId string) ([]Test, error) {
	if !bson.IsObjectIdHex(buildId) {
		return nil, nil
	}
	tests := []Test{}

	err := db.C("tests").Find(bson.M{"build_id": bson.ObjectIdHex(buildId)}).Sort("started").All(&tests)
	if err != nil {
		return nil, err
	}
	return tests, nil
}

func findBuildById(db *mgo.Database, id string) (*LogKeeperBuild, error) {
	if !bson.IsObjectIdHex(id) {
		fmt.Println("build not found: not object id hex")
		return nil, nil
	}
	build := &LogKeeperBuild{}

	err := db.C("builds").Find(bson.M{"_id": bson.ObjectIdHex(id)}).One(build)
	if err == mgo.ErrNotFound {
		fmt.Println("build not found: got mgo.ErrNotFound for ", id)
		return nil, nil
	}
	if err != nil {
		fmt.Println("error from find query: ", err)
		return nil, err
	}
	return build, nil
}

func findBuildByBuilder(db *mgo.Database, builder string, buildnum int) (*LogKeeperBuild, error) {
	build := &LogKeeperBuild{}

	err := db.C("builds").Find(bson.M{"builder": builder, "buildnum": buildnum}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}
