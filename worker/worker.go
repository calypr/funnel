// Package worker contains code which executes a task.
package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/calypr/funnel/compute/kubernetes/storageplan"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/storage"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/version"
)

// DefaultWorker is the default task worker, which follows a basic,
// sequential process of task initialization, execution, finalization,
// and logging.
type DefaultWorker struct {
	Executor         Executor
	Conf             *config.Worker
	Store            storage.Storage
	TaskReader       TaskReader
	ExecutionClaimer ExecutionClaimer
	EventWriter      events.Writer
	Command
}

var errTaskCanceled = errors.New("task canceled")

// Configuration of the task executor.
type Executor struct {
	StoragePlan *storageplan.Plan
	// "docker" or "kubernetes"
	Backend string
	// Kubernetes executor template
	Template string
	// Kubernetes persistent volume template
	PVTemplate string
	// Kubernetes persistent volume claim template
	PVCTemplate string
	// Funnel Server namespace
	Namespace string
	// Funnel Worker + Executor namespace
	JobsNamespace string
	// Kubernetes service account name
	ServiceAccount string
	// Kubernetes service account template
	ServiceAccountTemplate string
	// Kubernetes role template
	RoleTemplate string
	// Kubernetes role binding template
	RoleBindingTemplate string
	// NodeSelector for scheduling jobs onto specific nodes
	NodeSelector map[string]string
	// Tolerations for scheduling jobs onto specific nodes
	Tolerations []map[string]interface{}
	// Resources specifies the default resource requirements for Kubernetes jobs.
	Resources *config.KubernetesResources
}

// Run runs the Worker.
func (r *DefaultWorker) Run(pctx context.Context) (runerr error) {

	// The code here is verbose, but simple; mainly loops and simple error checking.
	//
	// The steps are:
	// - prepare the working directory
	// - map the task files to the working directory
	// - log the IP address
	// - set up the storage configuration
	// - validate input and output files
	// - download inputs
	// - run the steps (docker)
	// - upload the outputs
	var event *events.TaskWriter
	var mapper *FileMapper
	var run helper
	var task *tes.Task

	task, run.syserr = r.TaskReader.Task(pctx)
	// TODO if we failed to retrieve the task, we can't do anything useful.
	//      but, it's also difficult to report the failure usefully.
	if run.syserr != nil {
		// Best-effort: if the task ID is unknown there's no other reporting channel.
		_ = r.EventWriter.WriteEvent(pctx, events.NewSystemLog("unknown", 0, 0, "error",
			"failed to get task. ID unknown", map[string]string{
				"error": run.syserr.Error(),
			}))
		runerr = run.syserr
		return
	}
	localTask := false
	switch r.TaskReader.(type) {
	case *FileTaskReader, *Base64TaskReader:
		localTask = true
	}
	if !localTask && r.ExecutionClaimer == nil {
		return errors.New("execution claimer is required for non-local task readers")
	}

	// Task assignment moves a task to INITIALIZING before the node receives it.
	// Local task readers also use UNKNOWN or QUEUED before execution. Other states
	// indicate that this assignment is stale or the task is already in progress.
	switch task.GetState() {
	case tes.State_UNKNOWN, tes.State_QUEUED, tes.State_INITIALIZING:
	case tes.State_RUNNING:
		if !localTask {
			return r.waitForExecution(pctx)
		}
		return nil
	default:
		return
	}

	if plan := r.Executor.StoragePlan; plan != nil && plan.TaskID != task.Id {
		return fmt.Errorf("storage plan belongs to task %q, not %q", plan.TaskID, task.Id)
	}
	claimed := false
	if !localTask {
		claimed, run.syserr = r.ExecutionClaimer.ClaimExecution(pctx, task.Id)
		if run.syserr != nil {
			return fmt.Errorf("claim task execution: %w", run.syserr)
		}
		if !claimed {
			return r.waitForExecution(pctx)
		}
	}

	// set up task specific utilities
	event = events.NewTaskWriter(task.GetId(), 0, r.EventWriter)
	mapper = NewFileMapper(filepath.Join(r.Conf.WorkDir, task.GetId()))
	if r.Conf.ScratchPath != "" {
		scratchAbsDir, err := filepath.Abs(r.Conf.ScratchPath)
		if err != nil {
			return err
		}
		mapper.ScratchDir = filepath.Join(scratchAbsDir, filepath.Join(r.Conf.WorkDir, task.GetId()))
	}

	_ = event.Info("Version", version.LogFields()...)
	if !claimed {
		_ = event.State(tes.State_INITIALIZING)
	}
	_ = event.StartTime(time.Now())

	if name, err := os.Hostname(); err == nil {
		_ = event.Metadata(map[string]string{"hostname": name})
	}

	// TODO: Increment taskgroup waitgroup, defer done on waitgroup
	// Decrement taskgroup waitgroup on exit to make sure that the final logging step is called
	// before the worker is closed (and the backend database connection is closed).
	//
	// Run the final logging/state steps in a deferred function
	// to ensure they always run, even if there's a missed error.
	defer func() {
		_ = event.EndTime(time.Now())
		var finalState tes.State
		switch {
		case errors.Is(context.Cause(run.ctx), errTaskCanceled):
			// The task was canceled.
			_ = event.Info("Canceled")
			finalState = tes.State_CANCELED
			runerr = errTaskCanceled
		case run.syserr != nil:
			// Something else failed
			_ = event.Error("System error", "error", run.syserr)
			finalState = tes.State_SYSTEM_ERROR
			runerr = run.syserr
		case run.execerr != nil:
			// One of the executors failed
			_ = event.Error("Exec error", "error", run.execerr)
			finalState = tes.State_EXECUTOR_ERROR
			runerr = run.execerr
		default:
			finalState = tes.State_COMPLETE
		}
		if err := event.State(finalState); err != nil {
			runerr = errors.Join(runerr, fmt.Errorf("recording final task state %s: %w", finalState, err))
		}

		// cleanup workdir
		if !r.Conf.LeaveWorkDir {
			_ = mapper.Cleanup()
		}
	}()

	// Recover from panics
	defer handlePanic(func(e error) {
		fmt.Printf("%#v", e)
		run.syserr = e
	})

	ctx, stopPolling := r.pollForCancel(pctx)
	defer stopPolling()
	run.ctx = ctx

	// Prepare file mapper, which maps task file URLs to host filesystem paths
	if run.ok() {
		run.syserr = mapper.MapTask(task)
	}

	if run.ok() {
		run.syserr = r.validate(mapper)
	}

	// Download inputs
	if run.ok() {
		run.syserr = DownloadInputs(ctx, mapper.Inputs, r.Store, event, int(r.Conf.MaxParallelTransfers))
	}

	if run.ok() && !claimed {
		_ = event.State(tes.State_RUNNING)
	}

	// Create symlinks between the working directory and the Scratch Directory
	if run.ok() && r.Conf.ScratchPath != "" {
		err := mapper.CopyInputsToScratch(r.Conf.ScratchPath)
		if err != nil {
			return err
		}
		// Get the absolute path of the scratch directory
		scratchAbsDir, err := filepath.Abs(r.Conf.ScratchPath)
		if err != nil {
			return err
		}
		// For every volume in mapper, add the Scratch Path prefix to the host path
		for idx, v := range mapper.Volumes {
			v.HostPath = filepath.Join(scratchAbsDir, v.HostPath)
			mapper.Volumes[idx] = v
		}

	}

	if run.ok() {
		r.runExecutors(ctx, event, mapper, task, &run)
	}

	r.processOutputs(ctx, event, mapper, &run)

	return
}

func (r *DefaultWorker) waitForExecution(ctx context.Context) error {
	interval := time.Second
	if r.Conf != nil && r.Conf.PollingRate != nil && r.Conf.PollingRate.AsDuration() > 0 {
		interval = r.Conf.PollingRate.AsDuration()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := r.TaskReader.State(ctx)
		if err == nil && tes.TerminalState(state) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}

}

func (r *DefaultWorker) Close() {
	r.TaskReader.Close()
	r.EventWriter.Close()
}

// openLogs opens/creates the logs files for a step and updates those fields.
func (r *DefaultWorker) openStepLogs(mapper *FileMapper, s *stepWorker, d *tes.Executor) error {

	var stdin *os.File
	var stdout *os.File
	var stderr *os.File
	var err error

	// Find the path for task stdin
	if d.Stdin != "" {
		stdin, err = mapper.CreateHostFile(d.Stdin)
		s.Command.SetStdin(stdin)
		if err != nil {
			_ = s.Event.Error("Couldn't prepare log files", err)
			return err
		}
	}

	// Create file for task stdout
	if d.Stdout != "" {
		stdout, err = mapper.CreateHostFile(d.Stdout)
		s.Command.SetStdout(stdout)
		if err != nil {
			_ = s.Event.Error("Couldn't prepare log files", err)
			return err
		}
	}

	// Create file for task stderr
	if d.Stderr != "" {
		stderr, err = mapper.CreateHostFile(d.Stderr)
		s.Command.SetStderr(stderr)
		if err != nil {
			_ = s.Event.Error("Couldn't prepare log files", err)
			return err
		}
	}

	return nil
}

// Validate the downloads/uploads.
func (r *DefaultWorker) validate(mapper *FileMapper) error {
	// TODO need to switch on directory type and check list as well.
	for _, input := range mapper.Inputs {
		if err := storage.CheckGet(r.Store, input.Url); err != nil {
			return fmt.Errorf("Input download not supported by storage: %w", err)
		}
	}
	for _, output := range mapper.Outputs {
		if err := storage.CheckPut(r.Store, output.Url); err != nil {
			return fmt.Errorf("Output upload not supported by storage: %w", err)
		}
	}
	return nil
}

// Sanitizes the input string to avoid command injection.
// Only allows alphanumeric characters, dashes, underscores, and dots.
func (r *DefaultWorker) sanitizeValues(value string) string {
	// Replace anything that is not an alphanumeric character, dash, underscore, or dot
	re := regexp.MustCompile(`[^a-zA-Z0-9-_\.]`)
	return re.ReplaceAllString(value, "")
}

func (r *DefaultWorker) pollForCancel(pctx context.Context) (context.Context, func()) {
	taskctx, cancel := context.WithCancelCause(pctx)
	done := make(chan struct{})

	// Start a goroutine that polls the server to watch for a canceled state.
	// If a cancel state is found, "taskctx" is canceled.
	go func() {
		defer close(done)
		ticker := time.NewTicker(r.Conf.PollingRate.AsDuration())
		defer ticker.Stop()

		for {
			select {
			case <-taskctx.Done():
				return
			case <-ticker.C:
				state, _ := r.TaskReader.State(taskctx)
				if tes.TerminalState(state) {
					cancel(errTaskCanceled)
					return
				}
			}
		}
	}()
	return taskctx, func() {
		cancel(nil)
		<-done
	}
}

// runExecutors builds and runs each executor in the task. Kubernetes and
// Docker executors are constructed differently; failures are recorded on run
// and checked with run.ok() between executors.
func (r *DefaultWorker) runExecutors(ctx context.Context, event *events.TaskWriter, mapper *FileMapper, task *tes.Task, run *helper) {
	var resources = task.GetResources()
	if resources == nil {
		resources = &tes.Resources{}
	}

	for i, d := range task.GetExecutors() {
		// If a previous executor failed and its error was not to be ignored,
		// stop before creating any state (executor writer, log slot, etc.) for
		// this executor. Creating the writer above would otherwise emit an empty
		// executor log entry for an executor that never ran.
		if !run.ok() {
			break
		}

		var command = Command{
			Image:        d.Image,
			ShellCommand: d.Command,
			Volumes:      mapper.Volumes,
			Workdir:      d.Workdir,
			Env:          d.Env,
			Event:        event.NewExecutorWriter(uint32(i)),
		}

		var taskCommand TaskCommand

		if r.Executor.Backend == "kubernetes" {
			kcmd, kerr := r.newKubernetesCommand(ctx, event, task, d, i, command, resources)
			if kerr != nil {
				run.syserr = kerr
				break
			}
			taskCommand = kcmd
		} else {
			taskCommand = &DockerCommand{
				Volumes: mapper.Volumes,
				Workdir: d.Workdir,
				Name:    fmt.Sprintf("%s-%d", task.Id, i),
				// TODO make RemoveContainer configurable
				RemoveContainer: true,
				Event:           event.NewExecutorWriter(uint32(i)),
				DriverCommand:   r.Conf.Container.DriverCommand,
				RunCommand:      r.Conf.Container.RunCommand,
				PullCommand:     r.Conf.Container.PullCommand,
				StopCommand:     r.Conf.Container.StopCommand,
				Resources:       resources,
				Command:         command,
			}

			// TODO: Hide this behind explicit flag/option in configuration
			// if r.Conf.Container.EnableTags {
			// 	for k, v := range task.Tags {
			// 		safeTag := r.sanitizeValues(v)
			// 		taskCommand.Tags[k] = safeTag
			// 	}
			// }
		}

		s := &stepWorker{
			Conf:    r.Conf,
			Event:   event.NewExecutorWriter(uint32(i)),
			Command: taskCommand,
		}

		// Opens stdin/out/err files and updates those fields on "cmd".
		// Skip for Kubernetes: the executor runs in a separate pod and writes
		// stdout/stderr directly via its PVC mount. Creating host files here
		// would poison the Mountpoint inode, making the file unreadable by
		// the worker's mount instance (EPERM).
		if r.Executor.Backend != "kubernetes" {
			run.syserr = r.openStepLogs(mapper, s, d)
		}

		if run.ok() {
			err := s.Run(ctx)

			if err != nil {
				var k8sSystemErr *K8sSystemErr
				var execErr *K8sExecutorErr

				switch {
				// K8s System error
				case errors.As(err, &k8sSystemErr):
					run.syserr = err
				// K8s Executor error
				case errors.As(err, &execErr):
					run.execerr = err
				default:
					// Local (Docker) executor: classify by exit code. If the
					// container ran and exited (an exec.ExitError we can
					// extract a code from), the failure belongs to the task's
					// executor. Errors with no exit code at all — the docker
					// binary missing, a failure to start the driver — are the
					// worker's environment failing, i.e. a system error.
					if _, codeErr := getExitCode(err); codeErr == nil {
						run.execerr = err
					} else {
						run.syserr = err
					}
				}

				// If this executor declared that its errors should be ignored,
				// clear the executor error so subsequent executors still run.
				// System errors are never ignored.
				if run.execerr != nil && d.GetIgnoreError() {
					run.execerr = nil
				}
			}
		}
	}
}

// processOutputs finishes the task's file work after the executors: repairing
// symlinks, resolving wildcards, copying scratch outputs back, and uploading
// outputs. Upload runs even after an executor error — the output files are
// how a user diagnoses the failure — but is skipped on system errors where the
// worker itself is in a bad state.
func (r *DefaultWorker) processOutputs(ctx context.Context, event *events.TaskWriter, mapper *FileMapper, run *helper) {
	if run.syserr == nil {
		for _, output := range mapper.Outputs {
			fixLinks(mapper, output.Path)
		}
	}

	if run.syserr == nil {
		if err := resolveWildcards(mapper, r.Store); err != nil {
			if run.execerr != nil {
				_ = event.Error("Failed to resolve wildcard outputs after executor error", "error", err)
			} else {
				run.syserr = err
			}
		}
	}

	if run.syserr == nil && r.Conf.ScratchPath != "" {
		_ = mapper.CopyOutputsToWorkDir(r.Conf.ScratchPath)
	}

	var outputLog []*tes.OutputFileLog
	if run.syserr == nil {
		var uploadErr error
		outputLog, uploadErr = uploadOutputs(ctx, mapper.Outputs, r.Store, event, int(r.Conf.MaxParallelTransfers), mapper)
		if uploadErr != nil {
			if run.execerr != nil {
				// The executor already failed; treat upload errors as warnings so the
				// task is reported as EXECUTOR_ERROR rather than SYSTEM_ERROR.
				_ = event.Error("Failed to upload outputs after executor error", "error", uploadErr)
			} else {
				run.syserr = uploadErr
			}
		}
	}

	// unmap paths for OutputFileLog
	for _, o := range outputLog {
		o.Path = mapper.ContainerPath(o.Path)
	}

	if len(outputLog) > 0 {
		_ = event.Outputs(outputLog)
	}
}
