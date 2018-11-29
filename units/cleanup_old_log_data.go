package units

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/dependency"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2/bson"
)

const (
	cleanupJobsName = "cleanup-old-log-data-job"
	urlBase         = "https://evergreen.mongodb.com/rest/v2/tasks"
)

func init() {
	registry.AddJobType(cleanupJobsName,
		func() amboy.Job { return makeCleanupOldLogDataJob() })
}

type cleanupOldLogDataJob struct {
	testID   bson.ObjectId `bson:"test_id" json:"test_id" yaml:"test_id"`
	taskID   interface{}   `bson:"task_id" json:"task_id" yaml:"task_id"`
	job.Base `bson:"job_base" json:"job_base" yaml:"job_base"`
}

func NewCleanupOldLogDataJob(testID bson.ObjectId, taskID interface{}) amboy.Job {
	j := makeCleanupOldLogDataJob()
	j.testID = testID
	j.taskID = taskID
	j.SetID(fmt.Sprintf("%s.%s", cleanupJobsName, j.testID))
	return j
}

func makeCleanupOldLogDataJob() *cleanupOldLogDataJob {
	j := &cleanupOldLogDataJob{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    cleanupJobsName,
				Version: 0,
			},
		},
	}
	j.SetDependency(dependency.NewAlways())
	return j
}

// If the evergreen task is marked complete, delete the test and corresponding log objects
func (j *cleanupOldLogDataJob) Run(ctx context.Context) {
	defer j.MarkComplete()

	url := fmt.Sprintf("%s/%s", urlBase, j.taskID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		j.AddError(err)
		return
	}

	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		j.AddError(err)
		return
	}
	defer resp.Body.Close()

	payload, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		j.AddError(err)
		return
	}

	taskInfo := struct {
		Status string
	}{}

	if err = json.Unmarshal(payload, &taskInfo); err != nil {
		j.AddError(err)
		return
	}

	if taskInfo.Status != "success" {
		logkeeper.UpdateFailedTest(db, j.testID)
		if err != nil {
			j.AddError(errors.Wrap(err, "error updating failed status of test"))
		}
		return
	}

	db := db.GetDatabase()
	err = logkeeper.CleanupOldLogsByTest(db, j.testID)
	if err != nil {
		j.AddError(errors.Wrap(err, "error cleaning up old logs"))
	}
}
