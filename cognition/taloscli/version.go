package taloscli

import "github.com/spf13/cobra"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show TALOS version and build metadata.",
	Run: func(cmd *cobra.Command, args []string) {
		printCommandStatus(cmd.OutOrStdout(), "talos version", "success")
		printSection(cmd.OutOrStdout(), "results")
		printKV(cmd.OutOrStdout(), "version", currentTalosVersion())
		printKV(cmd.OutOrStdout(), "commit", currentTalosCommit())
		printKV(cmd.OutOrStdout(), "build_date", currentTalosBuildDate())
		printKV(cmd.OutOrStdout(), "runtime", runtimeDescriptor())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
