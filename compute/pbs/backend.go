// Package pbs contains code for accessing compute resources via PBS/Torque.
package pbs

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"

	"github.com/ohsu-comp-bio/funnel/compute"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// NewBackend returns a new PBS (Portable Batch System) HPCBackend instance.
func NewBackend(ctx context.Context, conf *config.Config, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*compute.HPCBackend, error) {
	if conf.PBS.TemplateFile != "" {
		content, err := os.ReadFile(conf.PBS.TemplateFile)
		if err != nil {
			return nil, fmt.Errorf("reading template: %v", err)
		}
		conf.PBS.Template = string(content)
	}

	b := &compute.HPCBackend{
		Name:          "pbs",
		SubmitCmd:     "qsub",
		CancelCmd:     "qdel",
		Conf:          conf,
		Template:      conf.PBS.Template,
		Event:         writer,
		Database:      reader,
		Log:           log,
		ExtractID:     extractID,
		MapStates:     mapStates,
		ReconcileRate: conf.PBS.ReconcileRate.AsDuration(),
	}

	if !conf.PBS.DisableReconciler {
		go b.Reconcile(ctx)
	}

	return b, nil
}

// extractID extracts the task id from the response returned by the `qsub` command.
// For PBS / Torque systems, `qsub` returns the task id
func extractID(in string) string {
	return in
}

type job struct {
	JobID      string `xml:"Job_Id"`
	JobState   string `xml:"job_state"`
	ExitStatus int    `xml:"exit_status"`
}

type xmlRecord struct {
	XMLName xml.Name `xml:"Data"`
	Job     []job
}

func mapStates(ids []string) ([]*compute.HPCTaskState, error) {
	var output []*compute.HPCTaskState

	idSet := make(map[string]interface{})
	for _, i := range ids {
		idSet[i] = nil
	}

	cmd := exec.Command("qstat", "-x")
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("qstat command failed: %v", err)
	}

	res := xmlRecord{}
	err = xml.Unmarshal(stdout, &res)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal qstat output: %v", err)
	}

	for _, j := range res.Job {
		if _, ok := idSet[j.JobID]; !ok {
			continue
		}
		state := stateMap[j.JobState]
		switch state {
		case "Complete":
			if j.ExitStatus == 0 {
				output = append(output, &compute.HPCTaskState{ID: j.JobID, TESState: tes.Complete, State: state})
			} else {
				output = append(output, &compute.HPCTaskState{
					ID: j.JobID, TESState: tes.SystemError, State: state, Reason: "Funnel worker exited with non-zero status",
				})
			}

		default:
			output = append(output, &compute.HPCTaskState{ID: j.JobID, TESState: pbsToTES[state], State: state})
		}
	}
	return output, nil
}

var stateMap = map[string]string{
	"C": "Complete",
	"E": "Exiting",
	"H": "Held",
	"Q": "Queued",
	"R": "Running",
	"S": "Suspended",
	"T": "Moving",
	"W": "Waiting",
}

var pbsToTES = map[string]tes.State{
	"Queued":    tes.Queued,
	"Running":   tes.Running,
	"Exiting":   tes.Running,
	"Held":      tes.Running,
	"Suspended": tes.Running,
	"Moving":    tes.Running,
	"Waiting":   tes.Running, // maybe should refer to Queued?
	"Complete":  tes.Complete,
}
