// Package gcp_batch contains code for accessing compute resources via Google Batch.
// ref: https://cloud.google.com/batch/docs
// ref: https://cloud.google.com/batch/docs/reference/rest
package gcp_batch

import (
	"context"
	"fmt"
	"strings"
	"time"

	batch "cloud.google.com/go/batch/apiv1"
	"cloud.google.com/go/batch/apiv1/batchpb"
	"cloud.google.com/go/logging"
	logadmin "cloud.google.com/go/logging/apiv2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"google.golang.org/api/option"
)

type Backend struct {
	client             client
	conf               *config.GCPBatch
	event              events.Writer
	database           tes.ReadOnlyServer
	log                *logger.Logger
	loggingClient      *logging.Client
	loggingAdminClient *logadmin.Client
	events.Backend
}

func NewBackend(ctx context.Context, conf *config.GCPBatch, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*Backend, error) {
	client, err := batch.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	// Initialize Cloud Logging client for log retrieval
	loggingClient, err := logging.NewClient(ctx, conf.Project)
	if err != nil {
		log.Warn("Failed to initialize Cloud Logging client, log retrieval will be disabled", "error", err)
		// Continue without logging client - other functionality will still work
		loggingClient = nil
	}

	// Initialize Cloud Logging Admin client for reading logs
	loggingAdminClient, err := logadmin.NewClient(ctx, option.WithQuotaProject(conf.Project))
	if err != nil {
		log.Warn("Failed to initialize Cloud Logging Admin client, log retrieval will be disabled", "error", err)
		loggingAdminClient = nil
	}

	b := &Backend{
		client:             client,
		conf:               conf,
		event:              writer,
		database:           reader,
		log:                log,
		loggingClient:      loggingClient,
		loggingAdminClient: loggingAdminClient,
	}

	if !conf.DisableReconciler {
		go b.reconcile(ctx)
	}

	return b, nil
}

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
	if b.loggingClient != nil {
		b.loggingClient.Close()
	}
	b.database.Close()
	b.event.Close()
}

// Example storage interface
func (b *Backend) NewStorage(conf config.Config) (*storage.GoogleCloud, error) {
	gs, nerr := storage.NewGoogleCloud(conf.GoogleStorage)
	if nerr != nil {
		return nil, fmt.Errorf("failed to configure Google Storage backend: %s", nerr)
	}

	return gs, nil
}

// extractBucketName extracts the bucket name from a gs:// URL
func extractBucketName(url string) string {
	if strings.HasPrefix(url, "gs://") {
		parts := strings.SplitN(strings.TrimPrefix(url, "gs://"), "/", 2)
		return parts[0]
	}
	return ""
}

// extractGCSPath extracts bucket and object path from a gs:// URL
func extractGCSPath(url string) (bucket string, objectPath string) {
	if strings.HasPrefix(url, "gs://") {
		urlPath := strings.TrimPrefix(url, "gs://")
		parts := strings.SplitN(urlPath, "/", 2)
		bucket = parts[0]
		if len(parts) > 1 {
			objectPath = parts[1]
		}
	}
	return bucket, objectPath
}

// validatePath checks if a path is safe to use in shell commands
func validatePath(path string) error {
	if path == "" {
		return nil // Empty paths are handled elsewhere
	}

	// Check for dangerous shell metacharacters
	dangerousChars := ";|&$`\n\r<>()"
	if strings.ContainsAny(path, dangerousChars) {
		return fmt.Errorf("path contains dangerous shell metacharacters: %s", path)
	}

	// Ensure path is absolute (starts with /)
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be absolute (start with /): %s", path)
	}

	return nil
}

// detectPathCollisions checks for multiple inputs/outputs using the same path
func detectPathCollisions(inputs []*tes.Input, outputs []*tes.Output) error {
	seen := make(map[string]string) // path -> url

	for _, input := range inputs {
		if input.Path == "" {
			continue
		}
		if existingURL, exists := seen[input.Path]; exists && existingURL != input.Url {
			return fmt.Errorf("path collision detected: %s used by both %s and %s",
				input.Path, existingURL, input.Url)
		}
		seen[input.Path] = input.Url
	}

	for _, output := range outputs {
		if output.Path == "" {
			continue
		}
		if existingURL, exists := seen[output.Path]; exists && existingURL != output.Url {
			return fmt.Errorf("path collision detected: %s used by both %s and %s",
				output.Path, existingURL, output.Url)
		}
		seen[output.Path] = output.Url
	}

	return nil
}

func (b *Backend) Submit(task *tes.Task) error {
	ctx := context.Background()

	// 1. Identify all unique buckets used by the task
	buckets := make(map[string]bool)
	extractBucketName := func(url string) string {
		if strings.HasPrefix(url, "gs://") {
			parts := strings.SplitN(strings.TrimPrefix(url, "gs://"), "/", 2)
			return parts[0]
		}
		return ""
	}

	for _, input := range task.Inputs {
		if bucket := extractBucketName(input.Url); bucket != "" {
			buckets[bucket] = true
		}
	}

	for _, output := range task.Outputs {
		if bucket := extractBucketName(output.Url); bucket != "" {
			buckets[bucket] = true
		}
	}

	// Mount all buckets to `/mnt/share/<BUCKET>` as volumes in the GCP Job Request
	var volumes []*batchpb.Volume
	for bucketName := range buckets {
		volumes = append(volumes, &batchpb.Volume{
			Source: &batchpb.Volume_Gcs{
				Gcs: &batchpb.GCS{
					RemotePath: bucketName,
				},
			},
			MountPath: fmt.Sprintf("/mnt/disks/%s", bucketName),
		})
	}

	// Runnables
	var runnables []*batchpb.Runnable

	for _, executor := range task.Executors {
		cmd := strings.Join(executor.Command, " ")

		if executor.Stdout != "" {
			// Redirect command output to the specified file path
			cmd = fmt.Sprintf("%s | tee %s", cmd, executor.Stdout)
		}

		runnable := &batchpb.Runnable{
			Executable: &batchpb.Runnable_Container_{
				Container: &batchpb.Runnable_Container{
					ImageUri: executor.Image,
					Commands: []string{"sh", "-c", cmd},
				},
			},
		}

		runnables = append(runnables, runnable)
	}

	// Resources
	resources := &batchpb.ComputeResource{}
	if task.Resources != nil {
		resources = &batchpb.ComputeResource{
			CpuMilli:  int64(task.Resources.CpuCores) * 1000,
			MemoryMib: int64(task.Resources.RamGb) * 1024,
		}
	}

	// TaskSpec
	taskSpec := &batchpb.TaskSpec{
		Runnables:       runnables,
		ComputeResource: resources,
		Environment:     &batchpb.Environment{},
		Volumes:         volumes,
	}

	// TaskGroup
	taskGroup := &batchpb.TaskGroup{
		TaskCount: 1,
		TaskSpec:  taskSpec,
	}

	// JobRequest
	req := &batchpb.CreateJobRequest{
		Parent: fmt.Sprintf("projects/%s/locations/%s", b.conf.Project, b.conf.Location),
		JobId:  task.Id,
		Job: &batchpb.Job{
			Name:       task.Id,
			Uid:        task.Id,
			TaskGroups: []*batchpb.TaskGroup{taskGroup},
			LogsPolicy: &batchpb.LogsPolicy{
				Destination: batchpb.LogsPolicy_CLOUD_LOGGING,
			},
		},
	}

	logger.Debug("GCP Batch Job Request", "request", req)

	resp, err := b.client.CreateJob(context.Background(), req)

	if err != nil {
		b.event.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError))
		b.event.WriteEvent(
			ctx,
			events.NewSystemLog(
				task.Id, 0, 0, "error",
				"error submitting task to GCP Batch",
				map[string]string{"error": err.Error()},
			),
		)
		return err
	}

	logger.Debug("Submitted task to GCP Batch",
		"taskID", task.Id,
		"gcpbatch_uid", resp.Uid,
		"gcpbatch_name", resp.Name)

	return b.event.WriteEvent(
		ctx, events.NewMetadata(task.Id, 0, map[string]string{
			"gcpbatch_uid":  resp.Uid,
			"gcpbatch_name": resp.Name,
		}),
	)
}

func (b *Backend) Cancel(ctx context.Context, taskID string) error {
	// TODO: Implement
	return nil
}

// Reconciler adapted from aws_batch/backend.go (worker-based executor) to "direct"-based executor model with GCP Batch.
//
// Currently the logic is to:
//  1. List all tasks in QUEUED, INITIALIZING, RUNNING states from the Funnel Database
//  2. Map all TES Task IDs to GCP Job IDs
//  3. List all GCP Jobs that have FAILED
//  4. Update the TES Task in the Funnel Database to SYSTEM_ERROR
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

			// For all Task states in QUEUED, INITIALIZING, RUNNING
			for _, s := range states {
				for {

					// List Tasks from Funnel Database
					lresp, err := b.database.ListTasks(ctx, &tes.ListTasksRequest{
						View:      tes.View_FULL.String(),
						State:     s,
						PageSize:  100,
						PageToken: pageToken,
					})
					if err != nil {
						b.log.Error("Calling ListTasks", err)
						continue ReconcileLoop
					}
					pageToken = lresp.NextPageToken

					// Map TES Task → GCP Batch Job
					tmap := make(map[string]*tes.Task)
					var jobs []*string
					for _, t := range lresp.Tasks {
						jobid := b.getTaskID(t)
						b.log.Debug("Checking task for GCP Batch job ID",
							"taskID", t.Id,
							"gcpbatch_uid", jobid,
							"state", t.State)
						if jobid != "" {
							tmap[jobid] = t
							jobs = append(jobs, aws.String(jobid))
						}
					}

					b.log.Debug("Tasks to reconcile",
						"count", len(jobs),
						"state", s)

					// Last page of jobs from the Funnel Database
					if len(jobs) == 0 {
						if pageToken == "" {
							break
						}
						continue
					}

					// List jobs from GCP Batch
					req := &batchpb.ListJobsRequest{
						Parent: fmt.Sprintf("projects/%s/locations/%s", b.conf.Project, b.conf.Location),
					}

					// Using an iterator here to page through GCP Batch jobs
					// Ref: https://pkg.go.dev/cloud.google.com/go/batch@v1.13.0/apiv1#example-Client.ListJobs
					it := b.client.ListJobs(ctx, req)

					for {
						j, err := it.Next()
						if err != nil {
							break
						}

						// If Job is in our list
						task, ok := tmap[j.Uid]
						if !ok {
							continue
						}

						// Handle FAILED jobs for now - basic state syncing
						if j.Status.State == batchpb.JobStatus_FAILED {
							// Fetch and write logs from Cloud Logging
							if b.loggingAdminClient != nil {
								if logs, err := b.fetchLogs(ctx, task.Id, j.Uid); err == nil {
									for _, log := range logs {
										b.event.WriteEvent(ctx, events.NewSystemLog(
											task.Id, 0, 0, log.Level, log.Msg, log.Fields,
										))
									}
								} else {
									b.log.Debug("Could not fetch logs for failed job",
										"taskID", task.Id,
										"jobName", j.Name,
										"error", err)
								}
							}

							if task.State != tes.ExecutorError {
								b.event.WriteEvent(ctx, events.NewState(task.Id, tes.ExecutorError))

								// Get failure reason from status events if available
								var failureReason string
								if j.Status != nil && len(j.Status.StatusEvents) > 0 {
									for _, event := range j.Status.StatusEvents {
										if event.Description != "" {
											failureReason = event.Description
											break
										}
									}
								}
								if failureReason == "" {
									failureReason = "GCP Batch job in FAILED state"
								}

								b.event.WriteEvent(
									ctx,
									events.NewSystemLog(
										task.Id, 0, 0, "error",
										failureReason,
										map[string]string{
											"gcpbatch_id":    j.Uid,
											"gcpbatch_name":  j.Name,
											"gcpbatch_state": j.Status.State.String(),
										},
									),
								)
							}
						}

						// Handle SUCCEEDED jobs
						if j.Status.State == batchpb.JobStatus_SUCCEEDED {

							// Fetch and write logs from Cloud Logging
							if b.loggingAdminClient != nil {
								if logs, err := b.fetchLogs(ctx, task.Id, j.Uid); err == nil {
									for _, log := range logs {
										b.event.WriteEvent(ctx, events.NewSystemLog(
											task.Id, 0, 0, log.Level, log.Msg, log.Fields,
										))
									}
								} else {
									b.log.Debug("Could not fetch logs for completed job",
										"taskID", task.Id,
										"jobName", j.Name,
										"error", err)
								}
							}

							if task.State != tes.Complete {
								b.event.WriteEvent(ctx, events.NewState(task.Id, tes.Complete))
							}
						}

						// Handle RUNNING jobs
						if j.Status.State == batchpb.JobStatus_RUNNING {

							// Fetch and write logs from Cloud Logging for running tasks
							if b.loggingAdminClient != nil {
								if logs, err := b.fetchLogs(ctx, task.Id, j.Uid); err == nil {
									for _, log := range logs {
										b.event.WriteEvent(ctx, events.NewSystemLog(
											task.Id, 0, 0, log.Level, log.Msg, log.Fields,
										))
									}
								} else {
									b.log.Debug("Could not fetch logs for running job",
										"taskID", task.Id,
										"jobName", j.Name,
										"error", err)
								}
							}

							if task.State != tes.Running {
								b.event.WriteEvent(ctx, events.NewState(task.Id, tes.Running))
							}
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

// Retreives Batch Job ID from Task metadata (created in #Submit) stored in Funnel Database
func (b *Backend) getTaskID(task *tes.Task) string {
	if task.Logs != nil && len(task.Logs) > 0 {

		if task.Logs[0].Metadata != nil {
			uid := task.Logs[0].Metadata["gcpbatch_uid"]

			if uid == "" {
				b.log.Debug("No gcpbatch_uid found for task", "taskID", task.Id)
				return ""
			}

			b.log.Debug("Retrieved gcpbatch_uid from task", "taskID", task.Id, "gcpbatch_uid", uid)
			return uid
		}

		b.log.Debug("No Metadata found for task", "taskID", task.Id)
		return ""

	}

	b.log.Debug("No Logs found for task", "taskID", task.Id)
	return ""
}
