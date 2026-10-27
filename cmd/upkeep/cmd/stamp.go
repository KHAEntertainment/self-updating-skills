package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/spf13/cobra"
)

var stampCmd = &cobra.Command{
	Use:   "stamp [dir]",
	Short: "Record frozen-block hashes as flat upkeep-frozen-* frontmatter keys",
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
		result, err := engine.Stamp(cmd.Context(), abs)
		if err != nil {
			// The script's stamp prints an error line to stderr and exits 2
			// when the skill has no SKILL.md.
			if missingSkill(err) {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: no SKILL.md at %s\n", filepath.Join(abs, "SKILL.md"))
				os.Exit(2)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		for _, l := range result.Lines {
			fmt.Fprintln(cmd.OutOrStdout(), l)
		}
		return nil
	},
}
