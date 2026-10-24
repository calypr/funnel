package run

import (
	"fmt"
	"os"

	"github.com/calypr/funnel/cmd/util"
	"github.com/spf13/pflag"
)

// *********************************************************************
// IMPORTANT:
// Usage/help docs are defined in usage.go.
// If you're updating flags, you probably need to update that file.
// *********************************************************************

type executor struct {
	cmd    string
	stdin  string
	stdout string
	stderr string
}

// flagVals captures values from CLI flag parsing
type flagVals struct {
	// Top-level flag values. These are not allowed to be redefined
	// by scattered tasks, to avoid complexity in avoiding
	// circular imports or nested scattering
	printTask    bool
	server       string
	scatterFiles []string
	exec         []string
	sh           []string

	// Internal tracking of executors. Not set by flags.
	execs []executor

	// Per-task flag values. These may be overridden by scattered tasks.
	name string
	// TODO all executors share the same container and workdir
	//      but could possibly be separate.
	workdir     string
	container   string
	description string
	stdin       []string
	stdout      []string
	stderr      []string
	preemptible bool
	wait        bool
	waitFor     []string
	inputs      []string
	inputDirs   []string
	outputs     []string
	outputDirs  []string
	content     []string
	environ     []string
	tags        []string
	volumes     []string
	zones       []string
	cpu         int
	ram         float64
	disk        float64
}

func newFlags(v *flagVals) *pflag.FlagSet {
	f := pflag.NewFlagSet("", pflag.ContinueOnError)
	// Disable usage because it's handled elsewhere (cmd.go)
	f.Usage = func() {}

	// These flags are separate because they are not allowed
	// in scattered tasks.
	//
	// Scattering is currently only allowed
	// at the top level in order to avoid any issues with circular
	// includes. If we want this to be per-task, it's possible,
	// but more work.
	f.StringVarP(&v.server, "server", "S", v.server, "")
	f.BoolVarP(&v.printTask, "print", "p", v.printTask, "")
	f.StringSliceVar(&v.scatterFiles, "scatter", v.scatterFiles, "")
	f.StringSliceVar(&v.sh, "sh", v.sh, "")
	f.StringSliceVar(&v.exec, "exec", v.exec, "")

	// Disable sorting in order to visit flags in primordial order below.
	// See buildExecs()
	f.SortFlags = false

	// General
	f.StringVarP(&v.container, "container", "c", v.container, "")
	f.StringVarP(&v.workdir, "workdir", "w", v.workdir, "")

	// Input/output
	f.StringSliceVarP(&v.inputs, "in", "i", v.inputs, "")
	f.StringSliceVarP(&v.inputDirs, "in-dir", "I", v.inputDirs, "")
	f.StringSliceVarP(&v.outputs, "out", "o", v.outputs, "")
	f.StringSliceVarP(&v.outputDirs, "out-dir", "O", v.outputDirs, "")
	f.StringSliceVar(&v.stdin, "stdin", v.stdin, "")
	f.StringSliceVar(&v.stdout, "stdout", v.stdout, "")
	f.StringSliceVar(&v.stderr, "stderr", v.stderr, "")
	f.StringSliceVarP(&v.content, "content", "C", v.content, "")

	// Resoures
	f.IntVar(&v.cpu, "cpu", v.cpu, "")
	f.Float64Var(&v.ram, "ram", v.ram, "")
	f.Float64Var(&v.disk, "disk", v.disk, "")
	f.StringSliceVar(&v.zones, "zone", v.zones, "")
	f.BoolVar(&v.preemptible, "preemptible", v.preemptible, "")

	// Other
	f.StringVarP(&v.name, "name", "n", v.name, "")
	f.StringVar(&v.description, "description", v.description, "")
	f.StringSliceVar(&v.volumes, "vol", v.volumes, "")
	f.StringSliceVar(&v.tags, "tag", v.tags, "")
	f.StringSliceVarP(&v.environ, "env", "e", v.environ, "")

	f.BoolVar(&v.wait, "wait", v.wait, "")
	f.StringSliceVar(&v.waitFor, "wait-for", v.waitFor, "")

	f.SetNormalizeFunc(util.NormalizeFlags)
	return f
}

// Set default flagVals
func defaultVals(vals *flagVals) {
	if vals.container == "" {
		vals.container = "alpine"
	}

	// Default name
	if vals.name == "" {
		vals.name = vals.execs[0].cmd
	}

	if vals.server == "" {
		envVal := os.Getenv("FUNNEL_SERVER")
		if envVal != "" {
			vals.server = envVal
		} else {
			vals.server = "http://localhost:8000"
		}
	}
}

func parseTopLevelArgs(vals *flagVals, args []string) error {
	flags := newFlags(vals)
	err := flags.Parse(args)

	if err != nil {
		return err
	}

	if len(flags.Args()) > 1 {
		return fmt.Errorf("--in, --out and --env args should have the form 'KEY=VALUE' not 'KEY VALUE'. Extra args: %s", flags.Args()[1:])
	}

	// Prepend command string given as positional argument to the args.
	// Prepend it as a flag so that it works better with parseTaskArgs().
	if len(flags.Args()) == 1 {
		shCmd := flags.Args()[0]
		args = append([]string{"--sh", shCmd}, args...)
	}
	if err := parseTaskArgs(vals, args); err != nil {
		return err
	}

	if len(vals.execs) == 0 {
		return fmt.Errorf("you must specify a command to run")
	}

	// Fill in empty values with defaults.
	defaultVals(vals)

	return nil
}

func parseTaskArgs(vals *flagVals, args []string) error {
	fl := newFlags(vals)
	if err := fl.Parse(args); err != nil {
		return err
	}
	return buildExecs(fl, vals, args)
}

// Visit flags to determine commands + stdin/out/err
// and build that information into vals.execs
//
// This is done so that multiple TES.Executors can be described by one
// "funnel run" command line.
func buildExecs(flags *pflag.FlagSet, vals *flagVals, args []string) error {
	vals.execs = nil
	var exec *executor
	if err := flags.ParseAll(args, func(f *pflag.Flag, value string) error {
		switch f.Name {
		case "sh", "exec":
			if exec != nil {
				// Append the current executor and start a new one.
				vals.execs = append(vals.execs, *exec)
			}
			if f.Name == "sh" {
				value = fmt.Sprintf("sh -c '%s'", value)
			}
			exec = &executor{
				cmd: value,
			}
		case "stdout":
			exec.stdout = value
		case "stderr":
			exec.stderr = value
		case "stdin":
			exec.stdin = value
		}
		return nil
	}); err != nil {
		return err
	}
	if exec != nil {
		vals.execs = append(vals.execs, *exec)
	}
	return nil
}
