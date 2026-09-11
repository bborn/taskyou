package main

import (
	osexec "os/exec"
	"testing"
)

// execInTmux hands the re-executed command line to a shell; each argument must
// come out the other side exactly as it went in.
func TestShellQuoteSurvivesShell(t *testing.T) {
	for _, arg := range []string{
		"#5187",
		"https://github.com/org/repo/pull/3482/files?w=1&a=b#r1",
		"it's got 'quotes' and $HOME and `ticks`",
		"draft offers",
		"",
	} {
		out, err := osexec.Command("sh", "-c", "printf %s "+shellQuote(arg)).Output()
		if err != nil {
			t.Fatalf("sh with %q: %v", arg, err)
		}
		if string(out) != arg {
			t.Errorf("shell turned %q into %q", arg, out)
		}
	}
}
