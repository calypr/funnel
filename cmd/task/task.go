package task

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/calypr/funnel/cmd/util"
	"github.com/calypr/funnel/tes"
	"github.com/spf13/cobra"
)

// NewCommand returns the "task" subcommands.
func NewCommand() *cobra.Command {
	cmd, _ := newCommandHooks()
	return cmd
}

func newCommandHooks() (*cobra.Command, *hooks) {

	h := &hooks{
		Create: Create,
		Get:    Get,
		List:   List,
		Cancel: Cancel,
		Wait:   Wait,
	}

	var (
		defaultTesServer = "http://localhost:8000"
		tesServer        string
	)

	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"tasks"},
		Short:   "Make API calls to a TES server.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if tesServer == "" {
				if val := os.Getenv("FUNNEL_SERVER"); val != "" {
					tesServer = val
				} else {
					tesServer = defaultTesServer
				}
			}
		},
	}
	cmd.SetGlobalNormalizationFunc(util.NormalizeFlags)
	f := cmd.PersistentFlags()
	f.StringVarP(&tesServer, "server", "S", tesServer, fmt.Sprintf("Server address (or FUNNEL_SERVER; default %s)", defaultTesServer))

	create := &cobra.Command{
		Use:   "create [task.json ...]",
		Short: "Create one or more tasks to run on the server.",
		Long:  `Tasks may be piped to stdin, e.g. "python generate_tasks.py | funnel task create"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectionHint(h.Create(tesServer, args, util.StdinPipe(), cmd.OutOrStdout()))
		},
	}

	var (
		pageToken   string
		pageSize    int32
		listAll     bool
		listView    string
		stateFilter string
		tagsFilter  []string
		namePrefix  string
	)

	list := &cobra.Command{
		Use:   "list",
		Short: "List all tasks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectionHint(h.List(tesServer, listView, pageToken, stateFilter, tagsFilter, namePrefix, pageSize, listAll, cmd.OutOrStdout()))
		},
	}

	lf := list.Flags()
	lf.StringVarP(&listView, "view", "v", "basic", "Task view")
	lf.StringVarP(&pageToken, "page-token", "p", pageToken, "Page token")
	lf.StringVar(&stateFilter, "state", stateFilter, "State filter")
	lf.StringSliceVar(&tagsFilter, "tag", tagsFilter, "Tag filter. May be used multiple times to specify more than one tag")
	lf.StringVar(&namePrefix, "name-prefix", namePrefix, "Name prefix")
	lf.Int32VarP(&pageSize, "page-size", "s", pageSize, "Page size")
	lf.BoolVar(&listAll, "all", listAll, "List all tasks")

	var getView string
	get := &cobra.Command{
		Use:   "get [taskID ...]",
		Short: "Get one or more tasks by ID.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectionHint(h.Get(tesServer, args, getView, cmd.OutOrStdout()))
		},
	}

	gf := get.Flags()
	gf.StringVarP(&getView, "view", "v", "full", "Task view")

	cancel := &cobra.Command{
		Use:   "cancel [taskID ...]",
		Short: "Cancel one or more tasks by ID.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectionHint(h.Cancel(tesServer, args, cmd.OutOrStdout()))
		},
	}

	wait := &cobra.Command{
		Use:   "wait [taskID...]",
		Short: "Wait for one or more tasks to complete.\n",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectionHint(h.Wait(tesServer, args))
		},
	}

	cmd.AddCommand(create, get, list, cancel, wait)
	return cmd, h
}

func connectionHint(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return fmt.Errorf("%w\nCheck that the server is running and the address is correct; use --server or FUNNEL_SERVER to set it.", err)
	}
	return err
}

type hooks struct {
	Create func(server string, messages []string, r io.Reader, w io.Writer) error
	Get    func(server string, ids []string, view string, w io.Writer) error
	List   func(server, view, pageToken, stateFilter string, tagsFilter []string, namePrefix string, pageSize int32, all bool, w io.Writer) error
	Cancel func(server string, ids []string, w io.Writer) error
	Wait   func(server string, ids []string) error
}

func getTaskState(str string) (tes.State, error) {
	if str == "" {
		return tes.Unknown, nil
	}
	i, ok := tes.State_value[strings.ToUpper(str)]
	if !ok {
		return tes.Unknown, fmt.Errorf("Unknown task state: %s. Valid states: ['queued', 'initializing', 'running', 'canceled', 'complete', 'system_error', 'executor_error']", str)
	}
	return tes.State(i), nil
}

func getTaskView(taskView string) (int32, error) {
	taskView = strings.ToUpper(taskView)
	var view int32
	var ok bool
	view, ok = tes.View_value[taskView]
	if !ok {
		return view, fmt.Errorf("Unknown task view: %s. Valid task views: ['basic', 'minimal', 'full']", taskView)
	}
	return view, nil
}
