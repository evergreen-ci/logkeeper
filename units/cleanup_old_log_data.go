package units

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/dependency"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
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
	BuildID  string      `bson:"build_id" json:"build_id" yaml:"build_id"`
	TaskID   interface{} `bson:"task_id" json:"task_id" yaml:"task_id"`
	job.Base `bson:"job_base" json:"job_base" yaml:"job_base"`
}

func NewCleanupOldLogDataJob(buildID string, taskID interface{}) amboy.Job {
	j := makeCleanupOldLogDataJob()
	j.BuildID = buildID
	j.TaskID = taskID
	j.SetID(fmt.Sprintf("%s.%s.%s", cleanupJobsName, j.BuildID, j.TaskID))
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

	if apiUser == "" {
		j.AddError(errors.New("cannot run job without a user defined"))
		return
	}

	client := utility.GetDefaultHTTPRetryableClient()
	defer utility.PutHTTPClient(client)
	url := fmt.Sprintf("%s/%s", urlBase, j.TaskID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		j.AddError(err)
		return
	}

	req = req.WithContext(ctx)
	req.Header.Add("Api-User", apiUser)
	req.Header.Add("Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		j.AddError(err)
		return
	}
	defer resp.Body.Close()

	taskInfo := struct {
		Status string `json:"status"`
	}{}

	if resp.StatusCode == 200 {
		if err = utility.ReadJSON(resp.Body, &taskInfo); err != nil {
			j.AddError(errors.Wrapf(err, "problem reading response from server for [task='%s' build='%s']", j.TaskID, j.BuildID))
			return
		}
	} else {
		errResp := struct {
			StatusCode int    `bson:"status" json:"status" yaml:"status"`
			Message    string `bson:"message" json:"message" yaml:"message"`
		}{}
		j.AddError(utility.ReadJSON(resp.Body, &errResp))

		grip.Error(message.Fields{
			"job":      j.ID(),
			"job_type": j.Type().Name,
			"task":     j.TaskID,
			"build":    j.BuildID,
			"code":     resp.StatusCode,
			"msg":      errResp.Message,
		})

		return
	}

	var num int

	if taskInfo.Status != "success" {
		err = logkeeper.UpdateFailedBuild(j.BuildID)
		if err != nil {
			j.AddError(errors.Wrapf(err, "error updating failed status of build %v", j.BuildID))
		}
	} else {
		num, err = logkeeper.CleanupOldLogsAndTestsByBuild(j.BuildID)
		if err != nil {
			j.AddError(errors.Wrapf(err, "error cleaning up old logs [%d]", num))
		}
	}

	grip.Info(message.Fields{
		"job_type": j.Type().Name,
		"op":       "deletion complete",
		"task":     j.TaskID,
		"build":    j.BuildID,
		"errors":   j.HasErrors(),
		"job":      j.ID(),
		"num":      num,
		"status":   taskInfo.Status,
		"code":     resp.StatusCode,
	})
}
