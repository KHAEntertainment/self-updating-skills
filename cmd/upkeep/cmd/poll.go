package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/spf13/cobra"
)

var (
	pollSeedDocs bool
	pollRunner   string
	pollJSON     bool
)

var pollCmd = &cobra.Command{
	Use:   "poll [flags] <dirs>",
	Short: "Poll upstream versions and tracked docs, updating the sidecar",
	Long: `Poll resolves the latest upstream versions (npm view / git
ls-remote --tags) and fetches tracked doc pages, records snapshots in the
state database, and writes the sidecar (latest_seen, checked_at,
latest_sha) plus the rendered SKILL.md together.

Poll takes the same single-writer lease as update, so only one poll or
update may write skill files at a time. A losing process prints
"skipped: lease held" and exits 0. --seed-docs is the explicit step that
fills a missing docs.<page>.verified_sha baseline; it is never implied by
a plain poll or update.`,
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
		drift, err := engine.Poll(ctx, abs, engine.PollOptions{
			SeedDocs: pollSeedDocs,
			Runner:   pollRunner,
		})
		if errors.Is(err, engine.ErrLeaseHeld) {
			fmt.Fprintln(cmd.OutOrStdout(), "skipped: lease held")
			return nil
		}
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}

		if pollJSON {
			b, err := json.MarshalIndent(drift, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		for _, d := range drift {
			renderDrift(cmd, d)
		}
		return nil
	},
}

func renderDrift(cmd *cobra.Command, d engine.Drift) {
	out := cmd.OutOrStdout()
	switch {
	case d.SkipReason != "":
		fmt.Fprintf(out, "SKIP %s — %s\n", d.SkillDir, d.SkipReason)
	case d.BlockReason != "":
		fmt.Fprintf(out, "BLOCKED %s — %s\n", d.SkillDir, d.BlockReason)
	default:
		fmt.Fprintf(out, "POLLED %s (owner %s)\n", d.SkillDir, d.Owner)
	}
	for _, p := range d.Pins {
		if p.Error != "" {
			fmt.Fprintf(out, "  pin %s: ERROR %s\n", p.Name, p.Error)
			continue
		}
		fmt.Fprintf(out, "  pin %s: verified %s latest %s\n", p.Name, p.VerifiedFor, p.LatestSeen)
	}
	for _, dd := range d.Docs {
		if dd.Error != "" {
			fmt.Fprintf(out, "  page %s: ERROR %s\n", dd.Page, dd.Error)
			continue
		}
		state := "unchanged"
		if dd.Changed {
			state = "CHANGED"
		}
		fmt.Fprintf(out, "  page %s: verified %s latest %s (%s)\n", dd.Page, dd.VerifiedSHA, dd.LatestSHA, state)
	}
}

func init() {
	pollCmd.Flags().BoolVar(&pollSeedDocs, "seed-docs", false, "fill missing docs.<page>.verified_sha baselines from the current snapshot (explicit)")
	pollCmd.Flags().StringVar(&pollRunner, "runner", "local", "owner runner gating sidecar writes (ci|local)")
	pollCmd.Flags().BoolVar(&pollJSON, "json", false, "print the Drift list as JSON")
}
