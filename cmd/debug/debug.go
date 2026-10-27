package debug

import (
	"encoding/json"

	cmdutil "github.com/calypr/funnel/cmd/util"
	"github.com/calypr/funnel/config"
	"github.com/calypr/funnel/version"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

func NewCommand() *cobra.Command {
	var file string
	flags := config.EmptyConfig()
	cmd := &cobra.Command{
		Use: "debug", Short: "Print version and redacted configuration for troubleshooting",
		Long: "Print version and local configuration with known sensitive fields redacted. This does not query a running server. Review the output before sharing it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cmdutil.MergeConfigFileWithFlags(file, flags)
			if err != nil {
				return err
			}
			data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(c.Safe())
			if err != nil {
				return err
			}
			report := struct {
				Version string          `json:"version"`
				Config  json.RawMessage `json:"config"`
			}{Version: version.Version, Config: data}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		},
	}
	cmd.SetGlobalNormalizationFunc(cmdutil.NormalizeFlags)
	cmd.Flags().AddFlagSet(cmdutil.ServerFlags(flags, &file))
	return cmd
}
