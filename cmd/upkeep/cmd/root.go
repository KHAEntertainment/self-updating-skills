package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Version is the engine binary version. The shell hand-off compares this
// against 0.3.0, so it must keep the exact "upkeep X.Y.Z" one-line format.
const Version = "0.4.0"

var rootCmd = &cobra.Command{
	Use:     "upkeep",
	Short:   "Self-updating skill engine (check, render, stamp, poll, update, status)",
	Version: Version,
}

func init() {
	rootCmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(renderCmd)
	rootCmd.AddCommand(stampCmd)
	rootCmd.AddCommand(pollCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(statusCmd)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
