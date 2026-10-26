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
	checkStrict    bool
	checkJSON      bool
	checkThreshold int
)

var checkCmd = &cobra.Command{
	Use:   "check [flags] [dir]",
	Short: "Check a skill's freshness and integrity",
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
		var threshold *int
		if cmd.Flags().Changed("threshold") {
			threshold = &checkThreshold
		}
		report, err := engine.CheckWithThreshold(cmd.Context(), abs, threshold)
		if err != nil {
			return err
		}
		if checkJSON {
			b, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(b))
		} else {
			fmt.Print(engine.RenderText(report))
		}
		if checkStrict && (report.MissingSkill || report.Verdict == "STALE" || report.Verdict == "TAMPERED") {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	checkCmd.Flags().BoolVar(&checkStrict, "strict", false, "exit 1 when the verdict is STALE or TAMPERED")
	checkCmd.Flags().BoolVar(&checkJSON, "json", false, "print the CheckReport struct as JSON")
	checkCmd.Flags().IntVar(&checkThreshold, "threshold", 0, "override the freshness threshold in days")
}
