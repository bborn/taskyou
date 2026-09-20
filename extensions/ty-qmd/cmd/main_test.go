package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// testRoot mirrors main()'s command wiring without executing anything.
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "ty-qmd"}
	root.AddCommand(syncCmd(), searchCmd(), serveCmd(), indexProjectCmd(), statusCmd())
	return root
}

func findSub(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	cmd, _, err := root.Find([]string{name})
	if err != nil {
		t.Fatalf("find %s: %v", name, err)
	}
	if cmd == nil {
		t.Fatalf("subcommand %q not wired on root", name)
	}
	return cmd
}

// TestSyncDoesNotRegisterAllFlag is the regression guard for the bug fixed by
// removing the dead --all/-a flag. The flag was previously declared, parsed and
// advertised in --help, but its value was never read, so it was a no-op. This
// test fails if anyone re-adds the flag without wiring it up.
func TestSyncDoesNotRegisterAllFlag(t *testing.T) {
	sync := findSub(t, testRoot(), "sync")
	if f := sync.Flags().Lookup("all"); f != nil {
		t.Errorf("--all flag should not be registered on sync; got %q (usage: %q)", f.Name, f.Usage)
	}
	if f := sync.Flags().ShorthandLookup("a"); f != nil {
		t.Errorf("-a shorthand should not be registered on sync; got %q", f.Name)
	}
}

// TestSyncStillRegistersProjectFlag guards against accidentally removing the
// real, working --project/-p filter flag while cleaning up --all.
func TestSyncStillRegistersProjectFlag(t *testing.T) {
	sync := findSub(t, testRoot(), "sync")
	if f := sync.Flags().Lookup("project"); f == nil {
		t.Fatal("--project flag must remain registered on sync")
	}
	if f := sync.Flags().ShorthandLookup("p"); f == nil {
		t.Fatal("-p shorthand must remain registered for --project on sync")
	}
}
