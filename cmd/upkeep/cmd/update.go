package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/spf13/cobra"
)

var (
	updateDryRun bool
	updateRunner string
	updateJSON   bool
)

var updateCmd = &cobra.Command{
	Use:   "update [flags] <dirs>",
	Short: "Poll, plan, and apply auto-advances",
	Long: `Update runs the full cycle: take the update lease, poll upstream
and tracked docs, plan the auto-advance rules, and apply advances (write
sidecar → render → check --strict). A skill whose owner does not match
--runner is skipped and reported, never touched. If another process holds
the lease, this run prints "skipped: lease held" and exits 0.

--dry-run prints the plan and writes nothing: no sidecar, no files, no
database, no lease.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		abs := make([]string, len(args))
		for i, a := range args {
			p, err := filepath.Abs(a)
			if err != nil {
				return err
			}
			abs[i] = p
		}
		res, err := engine.Update(ctx, abs, engine.UpdateOptions{DryRun: updateDryRun, Runner: updateRunner})
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		if res.SkippedLease {
			fmt.Fprintln(cmd.OutOrStdout(), "skipped: lease held")
			return nil
		}

		if updateJSON {
			b, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		renderPlan(cmd, res)
		return nil
	},
}

func renderPlan(cmd *cobra.Command, res *engine.UpdateResult) {
	out := cmd.OutOrStdout()
	if res.Plan != nil {
		for _, sp := range res.Plan.Skills {
			switch sp.Status {
			case "advance":
				fmt.Fprintf(out, "ADVANCE %s\n", sp.SkillDir)
				for _, a := range sp.Advances {
					fmt.Fprintf(out, "  %s %s → %s\n", a.Pin, a.From, a.To)
				}
			case "draft":
				fmt.Fprintf(out, "DRAFT %s — %s\n", sp.SkillDir, sp.Reason)
				for _, p := range sp.DraftPins {
					fmt.Fprintf(out, "  pin %s\n", p)
				}
				for _, p := range sp.DraftPages {
					fmt.Fprintf(out, "  page %s\n", p)
				}
			case "blocked":
				fmt.Fprintf(out, "BLOCKED %s — %s\n", sp.SkillDir, sp.Reason)
			case "skip":
				fmt.Fprintf(out, "SKIP %s — %s\n", sp.SkillDir, sp.Reason)
			default:
				msg := sp.Reason
				if msg == "" {
					msg = "nothing to do"
				}
				fmt.Fprintf(out, "NO-CHANGE %s — %s\n", sp.SkillDir, msg)
			}
		}
	}
	if res.RunID != 0 {
		fmt.Fprintf(out, "run %d %s\n", res.RunID, res.RunOutcome)
	}
}

func init() {
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "print the plan; write no file, no sidecar, no database")
	updateCmd.Flags().StringVar(&updateRunner, "runner", "local", "owner runner (ci|local); skills with a different owner are skipped")
	updateCmd.Flags().BoolVar(&updateJSON, "json", false, "print the UpdateResult struct as JSON")
}
