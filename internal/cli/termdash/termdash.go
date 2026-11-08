package termdash

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var (
	defaultTesServer = "http://localhost:8000"
	tesServer        string
)

// Cmd represents the dashboard command.
var Cmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start a Funnel dashboard in your terminal.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if tesServer == "" {
			if val := os.Getenv("FUNNEL_SERVER"); val != "" {
				tesServer = val
			} else {
				tesServer = defaultTesServer
			}
		}
	},
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return termdash(tesServer)
	},
}

func init() {
	Cmd.Flags().StringVarP(&tesServer, "server", "S", tesServer, fmt.Sprintf("(default \"%s\")", defaultTesServer))
}

func termdash(tesHTTPServerAddress string) error {
	m, err := newDashboard(tesHTTPServerAddress)
	if err != nil {
		return fmt.Errorf("error initializing the dashboard: %v", err)
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func timeNow() string {
	return time.Now().Format("3:04PM")
}
