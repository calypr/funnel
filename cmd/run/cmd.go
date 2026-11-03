package run

import (
	"bufio"
	"errors"
	"fmt"
	"os"

	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/util/shellwords"
	"github.com/spf13/cobra"
)

// *********************************************************************
// IMPORTANT:
// Usage/help docs are defined in usage.go.
// If you're updating flags, you probably need to update that file.
// *********************************************************************

// Cmd represents the run command
var Cmd = &cobra.Command{
	Use:   "run 'CMD' [flags]",
	Short: "Run a task.",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := Run(args)
		if err != nil {
			usageErr := cmd.Usage()
			if usageErr != nil {
				return errors.Join(err, fmt.Errorf("writing usage: %w", usageErr))
			}
			return err
		}
		return err
	},
	DisableFlagParsing: true,
}

func init() {
	Cmd.SetUsageTemplate(usage)
}

// ParseString calls shellwords.Split(s) and passes those args to Parse().
func ParseString(s string) ([]*tes.Task, error) {
	args, err := shellwords.Split(s)
	if err != nil {
		return nil, err
	}
	return Parse(args)
}

// Parse task a list of CLI args/flags, and converts them to tasks.
func Parse(args []string) ([]*tes.Task, error) {

	vals := flagVals{}
	err := parseTopLevelArgs(&vals, args)
	if err != nil {
		return nil, err
	}

	// Scatter all vals into tasks
	tasks := []*tes.Task{}
	scattered, err := scatter(vals)
	if err != nil {
		return nil, err
	}
	for _, v := range scattered {
		// Parse inputs, outputs, environ, and tags from flagVals
		// and update the task.
		task, err := valsToTask(v)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// Run takes a list of CLI args/flags, converts them to tasks and runs them.
func Run(args []string) error {
	vals := flagVals{}
	err := parseTopLevelArgs(&vals, args)
	if err != nil {
		return err
	}

	cli, err := tes.NewClient(vals.server)
	if err != nil {
		return err
	}

	// TES HTTP client
	tg := taskGroup{
		printTask: vals.printTask,
		client:    cli,
	}

	// Scatter all vals into tasks
	scattered, serr := scatter(vals)
	if serr != nil {
		return serr
	}
	for _, v := range scattered {
		// Parse inputs, outputs, environ, and tags from flagVals
		// and update the task.
		task, err := valsToTask(v)
		if err != nil {
			return err
		}

		tg.runTask(task, v.wait, v.waitFor)
	}

	return tg.wait()
}

// scatter reads each line from each scatter file, extending "base" flagVals
// with per-scatter vals from each line.
func scatter(base flagVals) ([]flagVals, error) {
	if len(base.scatterFiles) == 0 {
		return []flagVals{base}, nil
	}

	out := []flagVals{}

	for _, sc := range base.scatterFiles {
		// Read each line of the scatter file.
		fh, err := os.Open(sc)
		if err != nil {
			return nil, fmt.Errorf("opening scatter file %s: %w", sc, err)
		}
		scanner := bufio.NewScanner(fh)
		for scanner.Scan() {
			// Per-scatter flags
			sp, err := shellwords.Split(scanner.Text())
			if err != nil {
				//nolint:errcheck // the scatter file is read-only; close is best-effort cleanup.
				_ = fh.Close()
				return nil, fmt.Errorf("parsing scatter file %s: %w", sc, err)
			}
			tv := base
			if err := parseTaskArgs(&tv, sp); err != nil {
				//nolint:errcheck // the scatter file is read-only; close is best-effort cleanup.
				_ = fh.Close()
				return nil, fmt.Errorf("parsing scatter file %s: %w", sc, err)
			}
			// Parse scatter file flags into new flagVals
			out = append(out, tv)
		}
		err = scanner.Err()
		if err != nil {
			//nolint:errcheck // the scatter file is read-only; close is best-effort cleanup.
			_ = fh.Close()
			return nil, fmt.Errorf("reading scatter file %s: %w", sc, err)
		}
		//nolint:errcheck // the scatter file is read-only; close is best-effort cleanup.
		_ = fh.Close()
	}
	return out, nil
}
