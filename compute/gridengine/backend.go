// Package gridengine contains code for accessing compute resources via Open Grid Engine.
package gridengine

import (
	"fmt"
	"os"
	"regexp"

	"github.com/ohsu-comp-bio/funnel/compute"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
)

// NewBackend returns a new Grid Engine HPCBackend instance.
func NewBackend(conf *config.Config, reader tes.ReadOnlyServer, writer events.Writer, log *logger.Logger) (*compute.HPCBackend, error) {
	if conf.GridEngine.TemplateFile != "" {
		content, err := os.ReadFile(conf.GridEngine.TemplateFile)
		if err != nil {
			return nil, fmt.Errorf("reading template: %v", err)
		}
		conf.GridEngine.Template = string(content)
	}

	return &compute.HPCBackend{
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
