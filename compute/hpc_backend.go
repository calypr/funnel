package compute

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"text/template"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util/fsutil"
)

// HPCBackend represents an HPCBackend such as HtCondor, Slurm, Grid Engine, etc.
type HPCBackend struct {
	Name      string
	SubmitCmd string
	CancelCmd string
	Template  string
	Conf      *config.Config
	Event     events.Writer
	Database  tes.ReadOnlyServer
	// ExtractID is responsible for extracting the task id from the response
	// returned by the SubmitCmd.
	ExtractID func(string) string
	// MapStates takes a list of backend specific ids and calls out to the backend
	// via (squeue, qstat, condor_q, etc) to get that tasks current state. These states
	// are mapped to TES states along with an optional reason for this mapping.
	// The Reconcile function can then use the response to update the task states
	// and system logs to report errors reported by the backend.
	MapStates     func([]string) ([]*HPCTaskState, error)
	ReconcileRate time.Duration
	Log           *logger.Logger
	events.Backend
}

// WriteEvent writes an event to the compute backend.
// Currently, only TASK_CREATED is handled, which calls Submit.
func (b *HPCBackend) WriteEvent(ctx context.Context, ev *events.Event) error {
	switch ev.Type {
	case events.Type_TASK_CREATED:
		return b.Submit(ctx, ev.GetTask())

	case events.Type_TASK_STATE:
		if ev.GetState() == tes.State_CANCELED {
			return b.Cancel(ctx, ev.Id)
		}
	}
	return nil
}

func (b *HPCBackend) Close() {}

// Submit submits a task via "qsub", "condor_submit", "sbatch", etc.
func (b *HPCBackend) Submit(ctx context.Context, task *tes.Task) error {
	submitPath, err := b.setupTemplatedHPCSubmit(ctx, task)
	if err != nil {
		return err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(b.SubmitCmd, submitPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		b.Event.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError))
		b.Event.WriteEvent(
			ctx,
			events.NewSystemLog(
				task.Id, 0, 0, "error",
				"error submitting task to "+b.Name,
				map[string]string{"error": err.Error(), "stderr": stderr.String(), "stdout": stdout.String()},
			),
		)
		return err
	}

	backendID := b.ExtractID(stdout.String())

	return b.Event.WriteEvent(
		ctx,
		events.NewMetadata(task.Id, 0, map[string]string{fmt.Sprintf("%s_id", b.Name): backendID}),
	)
}

// Cancel cancels a task via "qdel", "condor_rm", "scancel", etc.
func (b *HPCBackend) Cancel(ctx context.Context, taskID string) error {
	task, err := b.Database.GetTask(
		ctx, &tes.GetTaskRequest{Id: taskID, View: tes.View_BASIC.String()},
	)
	if err != nil {
		return err
	}

	backendID := getBackendTaskID(task, b.Name)
	if backendID == "" {
		return fmt.Errorf("no %s_id found in metadata for task %s", b.Name, taskID)
	}

	cmd := exec.Command(b.CancelCmd, backendID)
	return cmd.Run()
}

// Reconcile loops through tasks and checks the status from Funnel's database
// against the status reported by the backend (slurm, htcondor, grid engine, etc).
// This allows the backend to report system error's that prevented the worker
// process from running.
//
// Currently this handles a narrow set of cases:
//
// |---------------------|-----------------|--------------------|
// |    Funnel State     |  Backend State  |  Reconciled State  |
// |---------------------|-----------------|--------------------|
// |        QUEUED       |     FAILED      |    SYSTEM_ERROR    |
// |        QUEUED       | QUEUED/PENDING* |    SYSTEM_ERROR    |
// |  INITIALIZING       |     FAILED      |    SYSTEM_ERROR    |
// |       RUNNING       |     FAILED      |    SYSTEM_ERROR    |
//
// In this context a "FAILED" state is being used as a generic term that captures
// one or more terminal states for the backend.
//
// *QUEUED/PENDING: this captures the case where the scheduler has a task that is
// stuck in the queued state because the resource request that can never be fulfilled.
func (b *HPCBackend) Reconcile(ctx context.Context) {
	ticker := time.NewTicker(b.ReconcileRate)

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
					lresp, err := b.Database.ListTasks(ctx, &tes.ListTasksRequest{
						View:      tes.View_BASIC.String(),
						State:     s,
						PageSize:  100,
						PageToken: pageToken,
					})
					if err != nil {
						b.Log.Error("Calling ListTasks", err)
						continue ReconcileLoop
					}
					if lresp.NextPageToken != nil {
						pageToken = *lresp.NextPageToken
					}

					tmap := make(map[string]*tes.Task)
					ids := []string{}
					for _, t := range lresp.Tasks {
						bid := getBackendTaskID(t, b.Name)
						if bid != "" {
							tmap[bid] = t
							ids = append(ids, bid)
						}
					}

					if len(ids) == 0 {
						if pageToken == "" {
							break
						}
						continue
					}

					bmap, err := b.MapStates(ids)
					if err != nil {
						b.Log.Error("Calling MapStates", err)
						continue ReconcileLoop
					}
					for _, t := range bmap {
						// lookup task by backend specific ID
						task := tmap[t.ID]
						if task == nil {
							continue
						}

						if t.TESState == tes.SystemError {
							b.Event.WriteEvent(ctx, events.NewState(task.Id, tes.SystemError))
							b.Event.WriteEvent(
								ctx,
								events.NewSystemLog(
									task.Id, 0, 0, "error",
									b.Name+" reports system error for task",
									map[string]string{
										"error":           t.Reason,
										b.Name + "_id":    t.ID,
										b.Name + "_state": t.State,
									},
								),
							)
							if t.Remove {
								exec.Command(b.CancelCmd, t.ID).Run()
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

// setupTemplatedHPCSubmit sets up a task submission in a HPC environment with
// a shared file system. It generates a submission file based on a template for
// schedulers such as SLURM, HTCondor, SGE, PBS/Torque, etc.
func (b *HPCBackend) setupTemplatedHPCSubmit(ctx context.Context, task *tes.Task) (string, error) {
	var err error

	// TODO document that these working dirs need manual cleanup
	workdir := path.Join(b.Conf.Worker.WorkDir, task.Id)
	workdir, _ = filepath.Abs(workdir)
	err = fsutil.EnsureDir(workdir)
	if err != nil {
		return "", err
	}

	submitName := fmt.Sprintf("%s.submit", b.Name)

	submitPath := path.Join(workdir, submitName)
	f, err := os.Create(submitPath)
	if err != nil {
		return "", err
	}

	submitTpl, err := template.New(submitName).Parse(b.Template)
	if err != nil {
		return "", err
	}

	res := task.GetResources()
	if res == nil {
		res = &tes.Resources{}
	}

	var zone string
	zones := res.GetZones()
	if zones != nil {
		zone = zones[0]
	}

	var args string
	if ctx.Value("Config") != nil {
		conf := ctx.Value("Config").(*config.Config)
		configFile := filepath.Join(workdir, "config.yaml")
		err = config.ToYamlFile(conf, configFile)
		if err != nil {
			return "", err
		}
		args = fmt.Sprintf("--config %v", configFile)
	}
	err = submitTpl.Execute(f, map[string]interface{}{
		"TaskId":  task.Id,
		"WorkDir": workdir,
		"Cpus":    res.GetCpuCores(),
		"RamGb":   res.GetRamGb(),
		"DiskGb":  res.GetDiskGb(),
		"Zone":    zone,
		"Args":    args,
	})
	if err != nil {
		return "", err
	}
	f.Close()

	return submitPath, nil
}

func getBackendTaskID(task *tes.Task, backend string) string {
	logs := task.GetLogs()
	if len(logs) > 0 {
		metadata := logs[0].GetMetadata()
		return metadata[backend+"_id"]
	}
	return ""
}

// HPCTaskState is a structure used by Reconcile to represent the state of a task in Funnel
// and the HPC backend.
type HPCTaskState struct {
	ID       string
	TESState tes.State
	State    string
	Reason   string
	Remove   bool
}
