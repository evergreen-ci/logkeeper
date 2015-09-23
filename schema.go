package logkeeper

import (
	"fmt"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"regexp"
	"time"
	"strings"
)

const logKeeperDB = "logkeeper"

type Test struct {
	Id        bson.ObjectId     `bson:"_id"`
	BuildId   bson.ObjectId     `bson:"build_id"`
	BuildName string            `bson:"build_name"`
	Name      string            `bson:"name"`
	Command   string            `bson:"command"`
	Started   time.Time         `bson:"started"`
	Ended     *time.Time        `bson:"ended"`
	Info      map[string]string `bson:"info"`
	Failed    bool              `bson:"failed"`
	Phase     string            `bson:"phase"`
	Seq       int               `bson:"seq",omitempty`
}

type LogKeeperBuild struct {
	Id       bson.ObjectId `bson:"_id"`
	Builder  string        `bson:"builder"`
	BuildNum int           `bson:"buildnum"`
	Started  time.Time     `bson:"started"`
	Name     string        `bson:"name"`
	Info     interface{}   `bson:"info"`
	Phases   []string      `bson:"phases"`
	Seq      int           `bson:"seq",omitempty`
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

func findBuildNames(db *mgo.Database, ids []bson.ObjectId) map[bson.ObjectId]string {
	iter := db.C("builds").Find(bson.M{"_id": bson.M{"$in": ids}}).Iter()
	build := &LogKeeperBuild{}
	names := make(map[bson.ObjectId]string)
	for iter.Next(build) {
		names[build.Id] = build.Name
	}
	return names
}

func findTestNames(db *mgo.Database, ids []bson.ObjectId) map[bson.ObjectId]string {
	iter := db.C("tests").Find(bson.M{"_id": bson.M{"$in": ids}}).Iter()
	test := &Test{}
	names := make(map[bson.ObjectId]string)
	for iter.Next(test) {
		names[test.Id] = test.Name
	}
	return names
}

// getRegex returns text with whitespace trimmed if isExactMatch.
// Otherwise it returns the OR of all space-separated terms.
func getRegex(text string, isExactMatch bool) string {
	regex := ""
	if isExactMatch {
		regex = regexp.QuoteMeta(strings.TrimSpace(text))
	} else {
		words := strings.Fields(text)
		for i, word := range words {
			regex = regex + regexp.QuoteMeta(word)
			if i < len(words) - 1 {
				regex = regex + "|"
			}
		}
	}
	return regex
}

func findTotalTextSearchResults(db *mgo.Database, text string, isExactMatch bool) int {
	totalHolder := &Count{}
	db.C("logs").Pipe([]bson.M{
			{"$match": bson.M{"$text": bson.M{"$search": text}}},
			{"$unwind": "$lines"},
			{"$match": bson.M{"lines.1": bson.M{"$regex": getRegex(text, isExactMatch), "$options": "i"}}},
			{"$group": bson.M{"_id": bson.M{"build_id": "$build_id", "test_id": "$test_id"}}},
			{"$group": bson.M{"_id": "null", "count": bson.M{"$sum": 1}}},
			}).One(totalHolder)
	return totalHolder.Count
}

func findTextSearchQueryResults(db *mgo.Database, text string, isExactMatch bool, skip int, limit int) ([]TextSearchQueryResult, error) {
	results := make([]TextSearchQueryResult, 0, limit)
	err := db.C("logs").Pipe([]bson.M{
			{"$match": bson.M{"$text": bson.M{"$search": text}}},
			{"$unwind": "$lines"},
			{"$match": bson.M{"lines.1": bson.M{"$regex": getRegex(text, isExactMatch), "$options": "i"}}},
			{"$sort": bson.M{"lines.0": 1}}, 
			{"$group": bson.M{
				"_id": bson.M{"build_id": "$build_id", "test_id": "$test_id"},
				"started": bson.M{"$first": "$started"},
				"lines": bson.M{"$first": "$lines"},
				"count": bson.M{"$sum": 1}}},
			{"$project": bson.M{
				"build_id": "$_id.build_id",
				"test_id": "$_id.test_id", 
				"started": 1,
				"lines": 1, 
				"count": 1,
				"_id":0}},
			{"$sort": bson.M{"started": -1}},
			{"$skip": skip},
			{"$limit": limit},
			}).All(&results)
	return results, err
}
