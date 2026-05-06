// Package slurm contains code for accessing compute resources via Slurm.
package slurm

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/ohsu-comp-bio/funnel/compute"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// NewBackend returns a new Slurm HPCBackend instance.
func NewBackend(ctx context.Context, conf *config.Config, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*compute.HPCBackend, error) {
	if conf.Slurm.TemplateFile != "" {
		content, err := os.ReadFile(conf.Slurm.TemplateFile)
		if err != nil {
			return nil, fmt.Errorf("reading template: %v", err)
		}
		conf.Slurm.Template = string(content)
	}

	b := &compute.HPCBackend{
		Name:          "slurm",
		SubmitCmd:     "sbatch",
		CancelCmd:     "scancel",
		Conf:          conf,
		Template:      conf.Slurm.Template,
		Event:         writer,
		Database:      reader,
		Log:           log,
		ExtractID:     extractID,
		MapStates:     mapStates,
		ReconcileRate: conf.Slurm.ReconcileRate.AsDuration(),
	}

	if !conf.Slurm.DisableReconciler {
		go b.Reconcile(ctx)
	}

	return b, nil
}

// extractID extracts the task id from the response returned by the `sbatch` command.
// Example response:
// Submitted batch job 2
func extractID(in string) string {
	re := regexp.MustCompile("(Submitted batch job )([0-9]+)\n$")
	return re.ReplaceAllString(in, "$2")
}

func mapStates(ids []string) ([]*compute.HPCTaskState, error) {
	var output []*compute.HPCTaskState

	cmd := exec.Command("squeue", "--noheader", "--Format", "jobid,state,reason", "--job", strings.Join(ids, ","))
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("squeue command failed: %v", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 3 {
			return nil, fmt.Errorf("failed to parse output from squeue")
		}
		id, state, reason := parts[0], parts[1], parts[2]
		if state == "PENDING" {
			if reason == "PartitionConfig" {
				output = append(output, &compute.HPCTaskState{
					ID: id, TESState: tes.SystemError, State: state, Reason: "No suitable partition available", Remove: true,
				})
			} else {
				output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.Queued, State: state})
			}
		} else {
			output = append(output, &compute.HPCTaskState{ID: id, TESState: squeueStateMap[state], State: state})
		}
	}

	cmd = exec.Command("sacct", "--noheader", "--format", "jobid,state", "--job", strings.Join(ids, ","))
	stdout, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sacct command failed: %v", err)
	}

	scanner = bufio.NewScanner(bytes.NewReader(stdout))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			return nil, fmt.Errorf("failed to parse output from sacct")
		}
		id, state := parts[0], parts[1]
		output = append(output, &compute.HPCTaskState{ID: id, TESState: sacctStateMap[state], State: state})
	}

	return output, nil
}

// sacct states
// https://slurm.schedmd.com/sacct.html
var sacctStateMap = map[string]tes.State{
	"PENDING":     tes.Queued,
	"CONFIGURING": tes.Queued,
	"RUNNING":     tes.Running,
	"RESIZING":    tes.Running,
	"COMPLETING":  tes.Running,
	"SUSPENDED":   tes.Running,
	"COMPLETED":   tes.Complete,
	"CANCELLED":   tes.SystemError,
	"DEADLINE":    tes.SystemError,
	"FAILED":      tes.SystemError,
	"NODE_FAIL":   tes.SystemError,
	"PREEMPTED":   tes.SystemError,
	"TIMEOUT":     tes.SystemError,
}

// squeue states
// https://slurm.schedmd.com/squeue.html
var squeueStateMap = map[string]tes.State{
	"PENDING":      tes.Queued,
	"CONFIGURING":  tes.Queued,
	"RUNNING":      tes.Running,
	"COMPLETING":   tes.Running,
	"SUSPENDED":    tes.Running,
	"COMPLETED":    tes.Complete,
	"CANCELLED":    tes.SystemError,
	"STOPPED":      tes.SystemError,
	"FAILED":       tes.SystemError,
	"TIMEOUT":      tes.SystemError,
	"PREEMPTED":    tes.SystemError,
	"NODE_FAIL":    tes.SystemError,
	"REVOKED":      tes.SystemError,
	"SPECIAL_EXIT": tes.SystemError,
}
