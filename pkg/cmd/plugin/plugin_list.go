package plugin

import (
	"encoding/json"
	"strings"

	"github.com/dcos/dcos-cli/api"
	"github.com/dcos/dcos-cli/pkg/cli"
	"github.com/spf13/cobra"
)

// newCmdPluginList creates the `dcos plugin list` subcommand.
func newCmdPluginList(ctx api.Context) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List CLI plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := ctx.Cluster()
			if err != nil {
				return err
			}

			plugins := ctx.PluginManager(cluster).Plugins()

			if jsonOutput {
				enc := json.NewEncoder(ctx.Out())
				enc.SetIndent("", "    ")
				return enc.Encode(plugins)
			}

			table := cli.NewTable(ctx.Out(), []string{"NAME", "COMMANDS"})
			for _, plugin := range plugins {
				var commands []string
				for _, command := range plugin.Commands {
					commands = append(commands, command.Name)
				}
				table.Append([]string{plugin.Name, strings.Join(commands, " ")})
			}
			table.Render()
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print plugins in JSON format.")
	return cmd
}
