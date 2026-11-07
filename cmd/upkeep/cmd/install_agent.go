package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/spf13/cobra"
)

var (
	installUninstall bool
	installLink      bool
	installRepo      string
	installPlistDir  string
	installHarness   []string
	installHour      int
	installMinute    int
	installDryRun    bool
	installJSON      bool
)

var installAgentCmd = &cobra.Command{
	Use:   "install-agent",
	Short: "Install (or remove) the daily launchd agent for local-owned skills",
	Long: `Install writes a launchd agent (a plist in ~/Library/LaunchAgents with a
daily StartCalendarInterval) and loads it. The job pulls this repo with
"git pull --ff-only" so the ci-owned skills follow their owner runner
(decision P3), and runs "upkeep update --runner local" over the
local-owned skills under ~/.agents/skills.

The plist holds no secret. The API key is read from the user's keychain
through Enigma when the job runs; register it once with:

  enigma add ANTHROPIC_API_KEY --scope global --depository keychain --usage unattended

--link is the explicit switch for the link work: it makes every ci-owned
entry under ~/.agents/skills a symlink into this repo's skills/ and links
every owned skill into every harness skills dir (including Antigravity's
~/.gemini/config/skills, the only dir it reads). Existing harness symlinks
are never touched, so they keep resolving through the chain. --uninstall
removes only the links the install created and puts back a directory it
moved aside.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		opts := engine.AgentOptions{
			Repo:        installRepo,
			PlistDir:    installPlistDir,
			Link:        installLink,
			HarnessDirs: installHarness,
			Hour:        installHour,
			Minute:      installMinute,
			DryRun:      installDryRun,
		}
		var (
			res *engine.AgentResult
			err error
		)
		if installUninstall {
			res, err = engine.UninstallAgent(ctx, opts)
		} else {
			res, err = engine.InstallAgent(ctx, opts)
		}
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		if installJSON {
			b, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		renderAgentResult(cmd, res)
		return nil
	},
}

// renderAgentResult prints the install or uninstall outcome: what was
// written, what was linked, what was removed and how to kick the job.
func renderAgentResult(cmd *cobra.Command, res *engine.AgentResult) {
	out := cmd.OutOrStdout()
	verb := "installed"
	if res.Action == "uninstall" {
		verb = "removed"
	}
	fmt.Fprintf(out, "%s launchd agent %s\n", verb, res.Label)
	fmt.Fprintf(out, "  plist    %s (%s)\n", safeLine(res.Plist), res.PlistState)
	if res.Action == "install" {
		loaded := "no"
		if res.Loaded {
			loaded = "yes"
		}
		fmt.Fprintf(out, "  loaded   %s (launchctl print %s)\n", loaded, res.Label)
		fmt.Fprintf(out, "  kick     %s\n", safeLine(res.KickCommand))
	}
	for _, l := range res.SkillLinks {
		fmt.Fprintf(out, "  skill    %s → %s (%s)\n", safeLine(l.Path), safeLine(l.Target), l.Action)
		if l.Note != "" {
			fmt.Fprintf(out, "             %s\n", safeLine(l.Note))
		}
	}
	for _, l := range res.HarnessLinks {
		fmt.Fprintf(out, "  harness  %s → %s (%s)\n", safeLine(l.Path), safeLine(l.Target), l.Action)
		if l.Note != "" {
			fmt.Fprintf(out, "             %s\n", safeLine(l.Note))
		}
	}
	for _, p := range res.Removed {
		fmt.Fprintf(out, "  removed  %s\n", safeLine(p))
	}
	for _, n := range res.Notes {
		fmt.Fprintf(out, "  note     %s\n", safeLine(n))
	}
}

func init() {
	installAgentCmd.Flags().BoolVar(&installUninstall, "uninstall", false, "unload the agent, remove the plist and remove the links the install created")
	installAgentCmd.Flags().BoolVar(&installLink, "link", false, "explicit: switch the ci-owned skills to repo symlinks and link every owned skill into every harness skills dir")
	installAgentCmd.Flags().StringVar(&installRepo, "repo", "", "repo root that holds skills/ (default: the current directory)")
	installAgentCmd.Flags().StringVar(&installPlistDir, "plist-dir", "", "directory the plist is written to (default: ~/Library/LaunchAgents)")
	installAgentCmd.Flags().StringArrayVar(&installHarness, "harness-dir", nil, "extra harness skills dir to link into (repeatable)")
	installAgentCmd.Flags().IntVar(&installHour, "hour", 0, "daily run hour (default: 7)")
	installAgentCmd.Flags().IntVar(&installMinute, "minute", 0, "daily run minute (default: 17)")
	installAgentCmd.Flags().BoolVar(&installDryRun, "dry-run", false, "print what would change; write nothing")
	installAgentCmd.Flags().BoolVar(&installJSON, "json", false, "print the AgentResult struct as JSON")
}
