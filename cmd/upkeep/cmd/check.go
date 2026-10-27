package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/spf13/cobra"
)

// decimalInt parses integer flag values in base 10, mirroring Python's
// int() (pflag's IntVar parses base 0, so "010" would become 8 and "08"
// would be rejected).
type decimalInt int

func (d *decimalInt) Set(s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid argument %q for \"--threshold\" flag: %v", s, err)
	}
	*d = decimalInt(n)
	return nil
}

func (d *decimalInt) String() string { return strconv.Itoa(int(*d)) }
func (d *decimalInt) Type() string   { return "int" }

var (
	checkStrict    bool
	checkJSON      bool
	checkThreshold decimalInt
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
			v := int(checkThreshold)
			threshold = &v
		}
		report, err := engine.CheckWithThreshold(cmd.Context(), abs, threshold)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(1)
		}
		if checkJSON {
			b, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		} else {
			fmt.Fprint(cmd.OutOrStdout(), engine.RenderText(report))
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
	checkCmd.Flags().Var(&checkThreshold, "threshold", "override the freshness threshold in days")
}
