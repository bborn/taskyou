package main

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runCreateFlags parses args through `ty projects create` without running it,
// so only the flag rules are exercised.
func runCreateFlags(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newProjectsCreateCmd()
	ran := false
	cmd.Run = func(*cobra.Command, []string) { ran = true }
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err != nil && ran {
		t.Fatal("flag validation should reject before the command body runs")
	}
	return err
}

func TestProjectsCreate_RepoAndPathAreMutuallyExclusive(t *testing.T) {
	err := runCreateFlags(t, "myapp", "--path", "/tmp/myapp", "--repo", "owner/myapp")
	if err == nil {
		t.Fatal("--path together with --repo should be a usage error")
	}
	if !strings.Contains(err.Error(), "path") || !strings.Contains(err.Error(), "repo") {
		t.Errorf("the error should name both flags, got %q", err)
	}
}

func TestProjectsCreate_OneSourceIsRequired(t *testing.T) {
	err := runCreateFlags(t, "myapp")
	if err == nil {
		t.Fatal("creating a project needs either --path or --repo")
	}
	if !strings.Contains(err.Error(), "path") || !strings.Contains(err.Error(), "repo") {
		t.Errorf("the error should name both flags, got %q", err)
	}
}

func TestProjectsCreate_EitherSourceAloneIsAccepted(t *testing.T) {
	for _, args := range [][]string{
		{"myapp", "--path", "/tmp/myapp"},
		{"myapp", "--repo", "https://github.com/owner/myapp"},
	} {
		if err := runCreateFlags(t, args...); err != nil {
			t.Errorf("%v should be accepted, got %v", args, err)
		}
	}
}
