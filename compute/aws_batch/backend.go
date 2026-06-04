// Package aws_batch contains code for accessing compute resources via AWS Batch.
// ref: https://aws.amazon.com/batch
package aws_batch

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/batch"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	util "github.com/ohsu-comp-bio/funnel/util/aws"
)

// NewBackend returns a new local Backend instance.
func NewBackend(ctx context.Context, conf *config.AWSBatch, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*Backend, error) {
	sess, err := util.NewAWSSession(conf.AWSConfig)
	if err != nil {
		return nil, fmt.Errorf("error occurred creating batch client: %v", err)
	}

	b := &Backend{
		client:   batch.New(sess),
		conf:     conf,
		event:    writer,
		database: reader,
		log:      log,
	}

	if !conf.DisableReconciler {
		go b.reconcile(ctx)
	}

	return b, nil
}

// client is an interface for AWS Batch operations.
// It allows for mocking in tests.
type client interface {
	SubmitJob(input *batch.SubmitJobInput) (*batch.SubmitJobOutput, error)
	CancelJob(input *batch.CancelJobInput) (*batch.CancelJobOutput, error)
	DescribeJobsWithContext(ctx context.Context, input *batch.DescribeJobsInput, opts ...request.Option) (*batch.DescribeJobsOutput, error)
}

// Backend represents the local backend.
type Backend struct {
	client   client
	conf     *config.AWSBatch
	event    events.Writer
	database tes.ReadOnlyServer
	log      *logger.Logger
	events.Backend
}

// WriteEvent writes an event to the compute backend.
// Currently, only TASK_CREATED is handled, which calls Submit.
func (b *Backend) WriteEvent(ctx context.Context, ev *events.Event) error {
	switch ev.Type {
	case events.Type_TASK_CREATED:
		return b.Submit(ev.GetTask())

	case events.Type_TASK_STATE:
		if ev.GetState() == tes.State_CANCELED {
			return b.Cancel(ctx, ev.Id)
		}
	}
	return nil
}

func (b *Backend) Close() {
	b.database.Close()
	b.event.Close()
}

// Submit submits a task to the AWS batch service.
func (b *Backend) Submit(task *tes.Task) error {
	ctx := context.Background()

	req := &batch.SubmitJobInput{
		// JobDefinition: aws.String(b.jobDef),
		JobDefinition: aws.String(b.conf.JobDefinition),
		JobName:       aws.String(safeJobName(task.Name)),
		JobQueue:      aws.String(b.conf.JobQueue),
		Parameters: map[string]*string{
			// Include the taskID in the job parameters. This gets used by
			// the funnel 'worker run' cmd.
			"taskID": aws.String(task.Id),
		},
		ContainerOverrides: &batch.ContainerOverrides{},
	}

	// convert ram from GB to MiB
	if task.Resources != nil {
		ram := int64(task.Resources.RamGb * 953.674)
		if ram > 0 {
			req.ContainerOverrides.Memory = aws.Int64(ram)
			req.ContainerOverrides.ResourceRequirements = []*batch.ResourceRequirement{
				{
					Type:  aws.String("MEMORY"),
					Value: aws.String(strconv.FormatInt(ram, 10)),
				},
			}
		}

		vcpus := int64(task.Resources.CpuCores)
		if vcpus > 0 {
			req.ContainerOverrides.Vcpus = aws.Int64(vcpus)
		}
	}

	resp, err := b.client.SubmitJob(req)
	if err != nil {
		b.event.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError))
		b.event.WriteEvent(
			ctx,
			events.NewSystemLog(
				task.Id, 0, 0, "error",
				"error submitting task to AWSBatch",
				map[string]string{"error": err.Error()},
			),
		)
		return err
	}

	return b.event.WriteEvent(
		ctx, events.NewMetadata(task.Id, 0, map[string]string{"awsbatch_id": *resp.JobId}),
	)
}

// Cancel removes tasks from the AWS batch job queue.
func (b *Backend) Cancel(ctx context.Context, taskID string) error {
	task, err := b.database.GetTask(
		ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_BASIC.String()},
	)
	if err != nil {
		return err
	}

	// only cancel tasks in a QUEUED state
	if task.State != tes.State_QUEUED {
		return nil
	}

	backendID := getAWSTaskID(task)
	if backendID == "" {
		return fmt.Errorf("no AWS Batch ID found in metadata for task %s", taskID)
	}

	_, err = b.client.CancelJob(&batch.CancelJobInput{
		JobId:  aws.String(backendID),
		Reason: aws.String("User requested cancel"),
	})
	return err
}

// Reconcile loops through tasks and checks the status from Funnel's database
// against the status reported by AWS Batch. This allows the backend to report
// system error's that prevented the worker process from running.
//
// Currently this handles a narrow set of cases:
//
// |---------------------|-----------------|--------------------|
// |    Funnel State     |  Backend State  |  Reconciled State  |
// |---------------------|-----------------|--------------------|
// |        QUEUED       |     FAILED      |    SYSTEM_ERROR    |
// |  INITIALIZING       |     FAILED      |    SYSTEM_ERROR    |
// |       RUNNING       |     FAILED      |    SYSTEM_ERROR    |
//
// In this context a "FAILED" state is being used as a generic term that captures
// one or more terminal states for the backend.
func (b *Backend) reconcile(ctx context.Context) {
	ticker := time.NewTicker(b.conf.ReconcileRate.AsDuration())

ReconcileLoop:
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			pageToken := ""
			states := []tes.State{tes.Queued, tes.Initializing, tes.Running}
			for _, s := range states {
				for {
					lresp, err := b.database.ListTasks(ctx, &tes.ListTasksRequest{
						View:      tes.View_BASIC.String(),
						State:     s,
						PageSize:  100,
						PageToken: pageToken,
					})
					if err != nil {
						b.log.Error("Calling ListTasks", err)
						continue ReconcileLoop
					}
					pageToken = lresp.NextPageToken

					tmap := make(map[string]*tes.Task)
					var jobs []*string
					for _, t := range lresp.Tasks {
						jobid := getAWSTaskID(t)
						if jobid != "" {
							tmap[jobid] = t
							jobs = append(jobs, aws.String(jobid))
						}
					}

					if len(jobs) == 0 {
						if pageToken == "" {
							break
						}
						continue
					}

					resp, err := b.client.DescribeJobsWithContext(ctx, &batch.DescribeJobsInput{
						Jobs: jobs,
					})
					if err != nil {
						b.log.Error("Calling DescribeJobsWithContext", err)
						continue ReconcileLoop
					}

					for _, j := range resp.Jobs {
						task := tmap[*j.JobId]
						jstate := *j.Status

						if jstate == "FAILED" {
							b.event.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError))
							b.event.WriteEvent(
								ctx,
								events.NewSystemLog(
									task.Id, 0, 0, "error",
									"AWSBatch job in FAILED state",
									map[string]string{"error": *j.StatusReason, "awsbatch_id": *j.JobId},
								),
							)
						}
					}

					// continue to next page from ListTasks or break
					if pageToken == "" {
						break
					}
					time.Sleep(time.Millisecond * 100)
				}
			}
		}
	}
}

// AWS limits the characters allowed in job names,
// so replace invalid characters with underscores.
func safeJobName(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	return re.ReplaceAllString(s, "_")
}

func getAWSTaskID(task *tes.Task) string {
	logs := task.GetLogs()
	if len(logs) > 0 {
		metadata := logs[0].GetMetadata()
		if metadata != nil {
			return metadata["awsbatch_id"]
		}
	}
	return ""
}
