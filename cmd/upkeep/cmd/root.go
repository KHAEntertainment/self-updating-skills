package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Version is the engine binary version. The shell hand-off compares this
// against 0.3.0, so it must keep the exact "upkeep 0.3.0" one-line format.
const Version = "0.3.0"

var rootCmd = &cobra.Command{
	Use:     "upkeep",
	Short:   "Self-updating skill engine (check)",
	Version: Version,
}

func init() {
	rootCmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	rootCmd.AddCommand(checkCmd)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
