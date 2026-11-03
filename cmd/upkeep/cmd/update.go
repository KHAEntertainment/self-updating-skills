package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

Output reflects the actual per-skill results: a rolled-back skill prints
FAILED with its cause, never ADVANCE.

A skill whose content really changed goes through drafting: a model
(ANTHROPIC_API_KEY, model UPKEEP_MODEL or claude-opus-5-5) drafts redirect
rows, and each row is verified (Q quote, S schema, D doc history). Only a
draft whose every row is auto-accepted is written. Otherwise the skill
prints PR (a person must review), FALLBACK (a row failed verification) or
FAILED with a hard stop (a row contradicts a frozen block), nothing is
written, and the items are listed by "upkeep status". Drafting with no
API key is a failure. One skill's failure does not stop
the others, but the command exits 1 when any skill failed (or the lease
was lost mid-run).

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
			// J1: --json emits the stable skip shape (valid JSON), like poll;
			// exit stays 0 for the ordinary contention skip.
			if updateJSON {
				b, err := json.MarshalIndent(res, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "skipped: lease held")
			}
			return nil
		}

		if updateJSON {
			b, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			if res.RunOutcome == "failed" {
				os.Exit(1)
			}
			return nil
		}
		renderUpdate(cmd, res)
		if res.RunOutcome == "failed" {
			os.Exit(1)
		}
		return nil
	},
}

func renderUpdate(cmd *cobra.Command, res *engine.UpdateResult) {
	out := cmd.OutOrStdout()
	if len(res.Skills) == 0 && res.Plan != nil {
		renderPlanOnly(cmd, res.Plan)
		return
	}
	for _, sr := range res.Skills {
		switch sr.Status {
		case "written":
			fmt.Fprintf(out, "ADVANCE %s\n", sr.SkillDir)
			for _, a := range sr.Advances {
				fmt.Fprintf(out, "  %s %s → %s\n", a.Pin, a.From, a.To)
			}
			renderRows(out, sr)
		case "failed":
			msg := sr.Error
			if sr.Outcome != "" || len(sr.Rows) > 0 || strings.HasPrefix(msg, "drafting") {
				// Drafting errors can quote model or API output.
				msg = safeLine(msg)
			}
			fmt.Fprintf(out, "FAILED %s — %s\n", sr.SkillDir, msg)
			if sr.Reason != "" {
				fmt.Fprintf(out, "  (%s)\n", sr.Reason)
			}
			renderRows(out, sr)
		case "pr":
			fmt.Fprintf(out, "PR %s — %s\n", sr.SkillDir, safeLine(sr.Reason))
			renderRows(out, sr)
		case "fallback":
			fmt.Fprintf(out, "FALLBACK %s — %s\n", sr.SkillDir, safeLine(sr.Reason))
			renderRows(out, sr)
		case "skipped":
			fmt.Fprintf(out, "SKIPPED %s — %s\n", sr.SkillDir, sr.Reason)
		case "draft":
			fmt.Fprintf(out, "DRAFT %s — %s\n", sr.SkillDir, sr.Reason)
			for _, p := range sr.DraftPins {
				fmt.Fprintf(out, "  pin %s\n", p)
			}
		case "blocked":
			fmt.Fprintf(out, "BLOCKED %s — %s\n", sr.SkillDir, sr.Reason)
		case "skip":
			fmt.Fprintf(out, "SKIP %s — %s\n", sr.SkillDir, sr.Reason)
		default:
			msg := sr.Reason
			if msg == "" {
				msg = "nothing to do"
			}
			fmt.Fprintf(out, "NO-CHANGE %s — %s\n", sr.SkillDir, msg)
		}
	}
	if res.RunID != 0 {
		fmt.Fprintf(out, "run %d %s\n", res.RunID, res.RunOutcome)
	}
}

// renderRows prints each drafted row's outcome and checks. The evidence
// quote is left to --json and the state database.
func renderRows(out io.Writer, sr engine.SkillResult) {
	for _, rr := range sr.Rows {
		var checks []string
		for _, c := range rr.Checks {
			state := "fail"
			switch {
			case !c.Ran:
				state = "not run"
			case c.Pass:
				state = "pass"
			}
			checks = append(checks, c.Name+" "+state)
		}
		line := fmt.Sprintf("  row %q: %q → %q [%s] %s", rr.Row.ID, rr.Row.Old, rr.Row.New, strings.Join(checks, ", "), rr.Outcome)
		if rr.Reason != "" {
			line += " — " + safeLine(rr.Reason)
		}
		fmt.Fprintln(out, line)
	}
}

// safeLine prepares text that may quote model or upstream output for the
// terminal: whitespace runs become one space, and text that still holds a
// control character or quote is printed Go-quoted, so it cannot move the
// cursor or forge extra lines.
func safeLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if q := strconv.Quote(s); q != `"`+s+`"` {
		return q
	}
	return s
}

func renderPlanOnly(cmd *cobra.Command, plan *engine.UpdatePlan) {
	out := cmd.OutOrStdout()
	for _, sp := range plan.Skills {
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

func init() {
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "print the plan; write no file, no sidecar, no database")
	updateCmd.Flags().StringVar(&updateRunner, "runner", "local", "owner runner (ci|local); skills with a different owner are skipped")
	updateCmd.Flags().BoolVar(&updateJSON, "json", false, "print the UpdateResult struct as JSON")
}
