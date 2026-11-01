package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
	"github.com/spf13/cobra"
)

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show recent update runs",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		res, err := engine.Status(ctx, store.DefaultStatePath())
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		if statusJSON {
			b, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		if len(res.Runs) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no runs recorded")
			return nil
		}
		for _, r := range res.Runs {
			fmt.Fprintf(cmd.OutOrStdout(), "run %d %s %s → %s (%s)\n", r.ID, r.Runner, r.StartedAt, r.FinishedAt, r.Outcome)
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print the StatusResult struct as JSON")
}
