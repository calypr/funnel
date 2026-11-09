// Package gridengine contains code for accessing compute resources via Open Grid Engine.
package gridengine

import (
	"fmt"
	"os"
	"regexp"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/internal/compute/hpc"
	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/internal/logger"
	taskpolicy "github.com/calypr/funnel/internal/task"
)

// NewBackend returns a new Grid Engine HPCBackend instance.
func NewBackend(conf *config.Config, reader taskpolicy.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*hpc.Backend, error) {
	if conf.GridEngine.TemplateFile != "" {
		content, err := os.ReadFile(conf.GridEngine.TemplateFile)
		if err != nil {
			return nil, fmt.Errorf("reading template: %v", err)
		}
		conf.GridEngine.Template = string(content)
	}

	err := hpc.ValidateSubmissionTemplate("GridEngine.Template", conf.GridEngine.Template)
	if err != nil {
		return nil, err
	}

	return &hpc.Backend{
		Name:      "gridengine",
		SubmitCmd: "qsub",
		CancelCmd: "qdel",
		Conf:      conf,
		Template:  conf.GridEngine.Template,
		Event:     writer,
		Database:  reader,
		Log:       log,
		ExtractID: extractID,
		// grid engine backend doesnt support state reconciliation
		MapStates:     nil,
		ReconcileRate: 0,
	}, nil
}

// extractID extracts the task id from the response returned by the `qsub` command.
// Example response:
// Your job 1 ("test_job") has been submitted
func extractID(in string) string {
	re := regexp.MustCompile("(Your job )([0-9]+)( \\(\".*\"\\) has been submitted)\n$")
	return re.ReplaceAllString(in, "$2")
}
