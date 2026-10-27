// A minimal, display-only unified diff. No behaviour depends on its output;
// it exists so RenderResult.Diff can show what a render would change.
package render

import (
	"fmt"
	"strings"
)

// maxDiffCells bounds the LCS table (lines x lines); beyond it the diff is
// omitted rather than burning memory on a huge file.
const maxDiffCells = 25_000_000

// Diff returns a small unified diff between old and new (3 lines of
// context), or a one-line note when the inputs are too large.
func Diff(old, new string) string {
	a := splitDiffLines(old)
	b := splitDiffLines(new)
	if equalLines(a, b) {
		return ""
	}
	if len(a)*len(b) > maxDiffCells {
		return fmt.Sprintf("(%d and %d lines differ; diff omitted)\n", len(a), len(b))
	}

	// LCS length table.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	// Walk the table into a change list.
	var ops []diffOp
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}

	// Group ops into hunks with 3 context lines.
	const ctx = 3
	var sb strings.Builder
	sb.WriteString("--- SKILL.md\n+++ SKILL.md\n")
	start := 0
	for start < len(ops) {
		// find next change
		k := start
		for k < len(ops) && ops[k].kind == ' ' {
			k++
		}
		if k == len(ops) {
			break
		}
		hStart := k - ctx
		if hStart < start {
			hStart = start
		}
		// extend while changes stay within ctx of each other
		end := k
		lastChange := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				lastChange = end
			} else if end-lastChange > ctx*2 {
				break
			}
			end++
		}
		hEnd := lastChange + ctx + 1
		if hEnd > len(ops) {
			hEnd = len(ops)
		}
		// line numbers: count old-side and new-side lines in the hunk
		oldN, newN := 0, 0
		for _, o := range ops[hStart:hEnd] {
			if o.kind != '+' {
				oldN++
			}
			if o.kind != '-' {
				newN++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", lineNo(ops, hStart, '-'), oldN, lineNo(ops, hStart, '+'), newN)
		for _, o := range ops[hStart:hEnd] {
			sb.WriteByte(o.kind)
			sb.WriteString(o.text)
			sb.WriteByte('\n')
		}
		start = hEnd
	}
	return sb.String()
}

// diffOp is one diff line: context, removed or added.
type diffOp struct {
	kind byte // ' ', '-', '+'
	text string
}

// lineNo returns the 1-based line number on the given side at op index idx.
func lineNo(ops []diffOp, idx int, side byte) int {
	other := byte('-')
	if side == '-' {
		other = '+'
	}
	n := 0
	for k := 0; k < idx; k++ {
		if ops[k].kind != other {
			n++
		}
	}
	return n + 1
}

func splitDiffLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
