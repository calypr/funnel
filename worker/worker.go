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

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/version"
)

// DefaultWorker is the default task worker, which follows a basic,
// sequential process of task initialization, execution, finalization,
// and logging.
type DefaultWorker struct {
	Executor    Executor
	Conf        *config.Worker
	Store       storage.Storage
	TaskReader  TaskReader
	EventWriter events.Writer
	Command
}

// Configuration of the task executor.
type Executor struct {
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
		r.EventWriter.WriteEvent(pctx, events.NewSystemLog("unknown", 0, 0, "error",
			"failed to get task. ID unknown", map[string]string{
				"error": run.syserr.Error(),
			}))
		runerr = run.syserr
		return
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

	event.Info("Version", version.LogFields()...)
	event.State(tes.State_INITIALIZING)
	event.StartTime(time.Now())

	if name, err := os.Hostname(); err == nil {
		event.Metadata(map[string]string{"hostname": name})
	}

	// TODO: Increment taskgroup waitgroup, defer done on waitgroup
	// Decrement taskgroup waitgroup on exit to make sure that the final logging step is called
	// before the worker is closed (and the backend database connection is closed).
	//
	// Run the final logging/state steps in a deferred function
	// to ensure they always run, even if there's a missed error.
	defer func() {
		event.EndTime(time.Now())
		switch {
		case run.taskCanceled:
			// The task was canceled.
			event.Info("Canceled")
			event.State(tes.State_CANCELED)
			runerr = fmt.Errorf("task canceled")
		case run.syserr != nil:
			// Something else failed
			event.Error("System error", "error", run.syserr)
			event.State(tes.State_SYSTEM_ERROR)
			runerr = run.syserr
		case run.execerr != nil:
			// One of the executors failed
			event.Error("Exec error", "error", run.execerr)
			event.State(tes.State_EXECUTOR_ERROR)
			runerr = run.execerr
		default:
			event.State(tes.State_COMPLETE)
		}

		// cleanup workdir
		if !r.Conf.LeaveWorkDir {
			mapper.Cleanup()
		}
	}()

	// Recover from panics
	defer handlePanic(func(e error) {
		fmt.Printf("%#v", e)
		run.syserr = e
	})

	ctx := r.pollForCancel(pctx, func() { run.taskCanceled = true })
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

	if run.ok() {
		event.State(tes.State_RUNNING)
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

	// Run steps
	if run.ok() {
		var resources = task.GetResources()
		if resources == nil {
			resources = &tes.Resources{}
		}

		ignoreError := false
		for i, d := range task.GetExecutors() {
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
				resources = config.ApplyDefaultResources(resources, r.Executor.Resources)
				resourceLimits := config.GetResourceLimits(r.Executor.Resources)

				taskCommand = &KubernetesCommand{
					TaskId:         task.Id,
					JobId:          i,
					StdinFile:      d.Stdin,
					TaskTemplate:   r.Executor.Template,
					Namespace:      r.Executor.Namespace,
					JobsNamespace:  r.Executor.JobsNamespace,
					Resources:      resources,
					ResourceLimits: resourceLimits,
					Command:        command,
					NeedsPVC:       len(task.GetInputs()) > 0 || len(task.GetOutputs()) > 0,
					NodeSelector:   r.Executor.NodeSelector,
					Tolerations:    r.Executor.Tolerations,
					ServiceAccount: fmt.Sprintf("funnel-worker-sa-%s-%s", r.Executor.JobsNamespace, task.Id),
				}

				// Override ServiceAccountName if provided in Task Tags
				if saName, exists := task.Tags["_WORKER_SA"]; exists && saName != "" {
					taskCommand.(*KubernetesCommand).ServiceAccount = saName
				}

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
			if run.ok() || ignoreError {
				run.syserr = r.openStepLogs(mapper, s, d)
			}

			if run.ok() || ignoreError {
				err := s.Run(ctx)

				if err != nil {
					// Check if it's a Kubernetes system error
					// TODO: Change this to check the exit code
					var k8sSystemErr *K8sSystemErr
					var execErr *K8sExecutorErr

					switch {
					// K8s System error
					case errors.As(err, &k8sSystemErr):
						run.syserr = err
					// K8s Executor error
					case errors.As(err, &execErr):
						run.execerr = err
					// Local (Docker) Executor error
					default:
						run.execerr = err
					}
				}
			}

			ignoreError = d.GetIgnoreError()
		}
	}

	// Try to fix symlinks broken by docker filesystems.
	if run.ok() {
		for _, output := range mapper.Outputs {
			fixLinks(mapper, output.Path)
		}
	}

	if run.ok() {
		// Resolve wildcards in the output paths
		resolveWildcards(mapper)
	}

	if run.ok() && r.Conf.ScratchPath != "" {
		mapper.CopyOutputsToWorkDir(r.Conf.ScratchPath)
	}

	// Upload outputs
	var outputLog []*tes.OutputFileLog
	if run.ok() {
		outputLog, run.syserr = UploadOutputs(ctx, mapper.Outputs, r.Store, event, int(r.Conf.MaxParallelTransfers))
	}

	// unmap paths for OutputFileLog
	for _, o := range outputLog {
		o.Path = mapper.ContainerPath(o.Path)
	}

	if len(outputLog) > 0 {
		event.Outputs(outputLog)
	}

	return
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
			s.Event.Error("Couldn't prepare log files", err)
			return err
		}
	}

	// Create file for task stdout
	if d.Stdout != "" {
		stdout, err = mapper.CreateHostFile(d.Stdout)
		s.Command.SetStdout(stdout)
		if err != nil {
			s.Event.Error("Couldn't prepare log files", err)
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
		unsupported := r.Store.UnsupportedOperations(input.Url)
		if unsupported.Get != nil {
			return fmt.Errorf("Input download not supported by storage: %v", unsupported.Get)
		}
	}
	for _, output := range mapper.Outputs {
		unsupported := r.Store.UnsupportedOperations(output.Url)
		if unsupported.Put != nil {
			return fmt.Errorf("Output upload not supported by storage: %v", unsupported.Put)
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

func (r *DefaultWorker) pollForCancel(pctx context.Context, cancelCallback func()) context.Context {
	taskctx, cancel := context.WithCancel(pctx)

	// Start a goroutine that polls the server to watch for a canceled state.
	// If a cancel state is found, "taskctx" is canceled.
	go func() {
		ticker := time.NewTicker(r.Conf.PollingRate.AsDuration())
		defer ticker.Stop()

		for {
			select {
			case <-taskctx.Done():
				return
			case <-ticker.C:
				state, _ := r.TaskReader.State(taskctx)
				if tes.TerminalState(state) {
					cancel()
					cancelCallback()
				}
			}
		}
	}()
	return taskctx
}
