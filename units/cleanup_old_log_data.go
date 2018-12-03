package units

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/evergreen-ci/logkeeper"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/dependency"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/pkg/errors"
)

const (
	cleanupJobsName = "cleanup-old-log-data-job"
	urlBase         = "https://evergreen.mongodb.com/rest/v2/tasks"
)

var (
	apiUser = os.Getenv("EVG_API_USER")
	apiKey  = os.Getenv("EVG_API_KEY")
)

func init() {
	registry.AddJobType(cleanupJobsName,
		func() amboy.Job { return makeCleanupOldLogDataJob() })
}

type cleanupOldLogDataJob struct {
	BuildID  interface{} `bson:"build_id" json:"build_id" yaml:"build_id"`
	TaskID   interface{} `bson:"task_id" json:"task_id" yaml:"task_id"`
	job.Base `bson:"job_base" json:"job_base" yaml:"job_base"`
}

func NewCleanupOldLogDataJob(buildID, taskID interface{}) amboy.Job {
	j := makeCleanupOldLogDataJob()
	j.BuildID = buildID
	j.TaskID = taskID
	j.SetID(fmt.Sprintf("%s.%s", cleanupJobsName, j.BuildID))
	return j
}

func makeCleanupOldLogDataJob() *cleanupOldLogDataJob {
	j := &cleanupOldLogDataJob{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    cleanupJobsName,
				Version: 1,
			},
		},
	}
	j.SetDependency(dependency.NewAlways())
	return j
}

// If the evergreen task is marked complete, delete the test and corresponding log objects
func (j *cleanupOldLogDataJob) Run(ctx context.Context) {
	defer j.MarkComplete()

	url := fmt.Sprintf("%s/%s", urlBase, j.TaskID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		j.AddError(err)
		return
	}

	req = req.WithContext(ctx)
	req.Header.Add("Api-User", apiUser)
	req.Header.Add("Api-Key", apiKey)

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
		Status string `json:"status"`
	}{}

	if err = json.Unmarshal(payload, &taskInfo); err != nil {
		j.AddError(err)
		return
	}

	if taskInfo.Status != "success" {
		err := logkeeper.UpdateFailedTestsByBuildID(j.BuildID)
		if err != nil {
			j.AddError(errors.Wrap(err, "error updating failed status of test"))
		}
		return
	}

	err = logkeeper.CleanupOldLogsByBuild(j.BuildID)
	if err != nil {
		j.AddError(errors.Wrap(err, "error cleaning up old logs"))
	}
}
