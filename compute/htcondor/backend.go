// Package htcondor contains code for accessing compute resources via HTCondor.
package htcondor

import (
	"context"
	"encoding/json"
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

// NewBackend returns a new HTCondor backend instance.
func NewBackend(ctx context.Context, conf *config.Config, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*compute.HPCBackend, error) {
	if conf.HTCondor.TemplateFile != "" {
		content, err := os.ReadFile(conf.HTCondor.TemplateFile)
		if err != nil {
			return nil, fmt.Errorf("reading template: %v", err)
		}
		conf.HTCondor.Template = string(content)
	}

	b := &compute.HPCBackend{
		Name:          "htcondor",
		SubmitCmd:     "condor_submit",
		CancelCmd:     "condor_rm",
		Conf:          conf,
		Template:      conf.HTCondor.Template,
		Event:         writer,
		Database:      reader,
		Log:           log,
		ExtractID:     extractID,
		MapStates:     mapStates,
		ReconcileRate: conf.HTCondor.ReconcileRate.AsDuration(),
	}

	if !conf.HTCondor.DisableReconciler {
		go b.Reconcile(ctx)
	}

	return b, nil
}

// extractID extracts the task id from the response returned by the `condor_submit` command.
// Example response:
// Submitting job(s).
// 1 job(s) submitted to cluster 1.
func extractID(in string) string {
	re := regexp.MustCompile("(.*\\.\n.*)([0-9]+)\\.\n")
	return re.ReplaceAllString(in, "$2")
}

type record struct {
	ClusterId int //nolint
	JobStatus int
	ExitCode  int
}

func mapStates(ids []string) ([]*compute.HPCTaskState, error) {
	var output []*compute.HPCTaskState

	qcmd := exec.Command("condor_q", "-json", "-attributes", "ClusterId,JobStatus,ExitCode", strings.Join(ids, ","))
	qout, err := qcmd.Output()
	if err != nil {
		return nil, fmt.Errorf("condor_q command failed: %v", err)
	}

	qparsed := []record{}
	err = json.Unmarshal(qout, &qparsed)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal condor_q output: %v", err)
	}

	hcmd := exec.Command("condor_history", "-json", "-attributes", "ClusterId,JobStatus,ExitCode", strings.Join(ids, ","))
	hout, err := hcmd.Output()
	if err != nil {
		return nil, fmt.Errorf("condor_history command failed: %v", err)
	}

	hparsed := []record{}
	err = json.Unmarshal(hout, &hparsed)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal condor_history output: %v", err)
	}

	parsed := append(qparsed, hparsed...)

	for _, t := range parsed {
		id := fmt.Sprintf("%v", t.ClusterId)
		exitcode := t.ExitCode
		state := stateMap[t.JobStatus]

		switch state {
		case "Idle":
			stuck, _ := checkIdleStatus(id)
			if stuck {
				output = append(output, &compute.HPCTaskState{
					ID: id, TESState: tes.SystemError, State: state, Reason: "no machines matched the jobs's constraints", Remove: true,
				})
			} else {
				output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.Queued, State: state})
			}

		case "Running":
			output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.Running, State: state})

		case "Held":
			output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.Running, State: state})

		case "Removed":
			output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.SystemError, State: state, Reason: "htcondor job was removed"})

		case "Submission_err":
			output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.SystemError, State: state, Reason: "task encountered submission error"})

		case "Completed":
			if exitcode == 0 {
				output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.Complete, State: state})
			} else {
				output = append(output, &compute.HPCTaskState{ID: id, TESState: tes.SystemError, State: state, Reason: "Funnel worker exited with non-zero status"})
			}
		}
	}
	return output, nil
}

func checkIdleStatus(id string) (bool, error) {
	cmd := exec.Command("condor_q", "-analyze", id)
	stdout, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("'condor_q -analyze %s' command failed: %v", id, err)
	}
	msg := "No machines matched the jobs's constraints"
	if stdout != nil && strings.Contains(string(stdout), msg) {
		return true, err
	}
	return false, err
}

var stateMap = map[int]string{
	1: "Idle",
	2: "Running",
	3: "Removed",
	4: "Completed",
	5: "Held",
	6: "Submission_err",
}
