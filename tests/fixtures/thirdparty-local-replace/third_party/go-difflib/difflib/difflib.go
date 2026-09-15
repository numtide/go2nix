// Package difflib is a stub of github.com/pmezard/go-difflib/difflib with
// just the API testify/assert uses.
package difflib

import "strings"

type UnifiedDiff struct {
	A        []string
	FromFile string
	FromDate string
	B        []string
	ToFile   string
	ToDate   string
	Eol      string
	Context  int
}

func SplitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	lines[len(lines)-1] += "\n"
	return lines
}

func GetUnifiedDiffString(diff UnifiedDiff) (string, error) {
	return strings.Join(diff.A, "") + strings.Join(diff.B, ""), nil
}
