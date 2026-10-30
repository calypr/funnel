package version

import (
	"fmt"

	"github.com/calypr/funnel/version"
	"github.com/spf13/cobra"
)

// Cmd represents the "version" command
var Cmd = &cobra.Command{
	Use: "version",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), version.String()); err != nil {
			return fmt.Errorf("writing version output: %w", err)
		}
		return nil
	},
}
