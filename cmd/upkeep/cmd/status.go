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
	Short: "Show recent update runs and pending PR, fallback and hard-stop items",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		path := store.DefaultStatePath()
		res, err := engine.Status(ctx, path)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		pend, err := engine.Pending(ctx, path)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		if statusJSON {
			b, err := json.MarshalIndent(struct {
				*engine.StatusResult
				*engine.PendingResult
			}{res, pend}, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		out := cmd.OutOrStdout()
		if len(res.Runs) == 0 {
			fmt.Fprintln(out, "no runs recorded")
		}
		for _, r := range res.Runs {
			fmt.Fprintf(out, "run %d %s %s → %s (%s)\n", r.ID, safeLine(r.Runner), r.StartedAt, r.FinishedAt, safeLine(r.Outcome))
		}
		for _, f := range pend.Failures {
			fmt.Fprintf(out, "FAILED %s — %s\n", safeLine(f.SkillDir), safeLine(f.Error))
		}
		for _, v := range pend.Rows {
			var row struct {
				ID     string `json:"id"`
				Old    string `json:"old"`
				New    string `json:"new"`
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal([]byte(v.RowJSON), &row)
			row.Reason = safeLine(row.Reason)
			label := map[string]string{"pr": "PR", "fallback": "FALLBACK", "hard_stop": "HARD-STOP"}[v.Outcome]
			if row.ID == "" {
				fmt.Fprintf(out, "%s %s (run %d) — %s\n", label, safeLine(v.Skill), v.RunID, row.Reason)
				continue
			}
			fmt.Fprintf(out, "%s %s (run %d) row %q: %q → %q — %s\n", label, safeLine(v.Skill), v.RunID, row.ID, row.Old, row.New, row.Reason)
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print the StatusResult struct as JSON")
}
