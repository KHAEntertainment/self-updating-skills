package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/spf13/cobra"
)

var (
	renderCheck bool
	renderJSON  bool
)

var renderCmd = &cobra.Command{
	Use:   "render [--check] [dir]",
	Short: "Rewrite generated regions and owned frontmatter keys from upkeep.data.json",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		result, err := engine.Render(cmd.Context(), abs, renderCheck)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		if renderJSON {
			b, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		} else {
			if result.Changed && result.Diff != "" {
				fmt.Fprint(cmd.OutOrStdout(), result.Diff)
			}
			if renderCheck {
				if result.Changed {
					fmt.Fprintln(cmd.OutOrStdout(), "VERDICT: DRIFT — SKILL.md differs from upkeep.data.json (run: upkeep render)")
					os.Exit(1)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "VERDICT: UNCHANGED — output matches upkeep.data.json")
				return nil
			}
			if result.Changed {
				fmt.Fprintln(cmd.OutOrStdout(), "VERDICT: RENDERED — SKILL.md rewritten from upkeep.data.json")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "VERDICT: UNCHANGED — output matches upkeep.data.json")
			}
		}
		return nil
	},
}

func init() {
	renderCmd.Flags().BoolVar(&renderCheck, "check", false, "compute what would change without writing; exit 1 on drift")
	renderCmd.Flags().BoolVar(&renderJSON, "json", false, "print the RenderResult struct as JSON")
}

// missingSkill reports whether err is a stat failure for a missing
// SKILL.md; the script's stamp exits 2 with an error line in that case.
func missingSkill(err error) bool {
	var ee *engine.Error
	if errors.As(err, &ee) && ee.Op == "stat" {
		var perr *os.PathError
		if errors.As(ee.Err, &perr) && errors.Is(perr.Err, syscall.ENOENT) {
			return true
		}
		return os.IsNotExist(ee.Err)
	}
	return false
}
