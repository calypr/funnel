package cmd

import "github.com/spf13/cobra"

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate shell completion code",
}

var bash = &cobra.Command{
	Use:   "bash",
	Short: "Generate bash completion code",
	Long: `This command generates bash CLI completion code.
Add "source <(funnel completion bash)" to your bash profile.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RootCmd.GenBashCompletion(cmd.OutOrStdout())
	},
}

func init() {
	completionCmd.AddCommand(bash)
}
