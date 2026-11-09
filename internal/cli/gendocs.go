package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var genMarkdownCmd = &cobra.Command{
	Use:    "genmarkdown",
	Short:  "generate markdown formatted documentation for the funnel commands",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return doc.GenMarkdownTree(RootCmd, "./funnel-cmd-docs")
	},
}
