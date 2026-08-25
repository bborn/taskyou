package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRepoRef_AcceptedForms(t *testing.T) {
	want := RepoRef{Host: "github.com", Owner: "bborn", Name: "taskyou"}
	cases := []struct {
		in  string
		ssh bool
	}{
		{in: "https://github.com/bborn/taskyou"},
		{in: "https://github.com/bborn/taskyou/"},
		{in: "https://github.com/bborn/taskyou.git"},
		{in: "https://github.com/bborn/taskyou.git/"},
		{in: "http://github.com/bborn/taskyou"},
		{in: "  https://github.com/bborn/taskyou  "},
		{in: "git@github.com:bborn/taskyou.git", ssh: true},
		{in: "git@github.com:bborn/taskyou", ssh: true},
		{in: "ssh://git@github.com/bborn/taskyou.git", ssh: true},
		{in: "github.com/bborn/taskyou"},
		{in: "bborn/taskyou"},
		{in: "bborn/taskyou.git"},
	}
	for _, tc := range cases {
		got, err := ParseRepoRef(tc.in)
		if err != nil {
			t.Errorf("ParseRepoRef(%q) returned error: %v", tc.in, err)
			continue
		}
		if !got.SameRepo(want) {
			t.Errorf("ParseRepoRef(%q) = %+v, want same repo as %+v", tc.in, got, want)
		}
		if got.SSH != tc.ssh {
			t.Errorf("ParseRepoRef(%q).SSH = %v, want %v", tc.in, got.SSH, tc.ssh)
		}
	}
}

func TestParseRepoRef_Rejects(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"taskyou",
		"not a url",
		"https://github.com/bborn",
		"https://github.com/",
		"ftp://github.com/bborn/taskyou",
		"file:///etc/passwd",
		"bborn/taskyou; rm -rf /",
		"https://github.com/bborn/task you",
		"/bborn/",
		"-bborn/taskyou",
		"https://github.com/bborn/../../etc",
	}
	for _, in := range cases {
		if got, err := ParseRepoRef(in); err == nil {
			t.Errorf("ParseRepoRef(%q) = %+v, want error", in, got)
		}
	}
}

func TestParseRepoRef_DeepLinkNamesTheRepo(t *testing.T) {
	_, err := ParseRepoRef("https://github.com/bborn/taskyou/tree/main/internal")
	if err == nil {
		t.Fatal("expected an error for a URL pointing inside a repo")
	}
	if !strings.Contains(err.Error(), "bborn/taskyou") {
		t.Errorf("error should name the repo to use, got %q", err)
	}
}

func TestRepoRef_CloneURL(t *testing.T) {
	https, _ := ParseRepoRef("https://github.com/bborn/taskyou")
	if got, want := https.CloneURL(), "https://github.com/bborn/taskyou.git"; got != want {
		t.Errorf("CloneURL() = %q, want %q", got, want)
	}
	ssh, _ := ParseRepoRef("git@github.com:bborn/taskyou.git")
	if got, want := ssh.CloneURL(), "git@github.com:bborn/taskyou.git"; got != want {
		t.Errorf("ssh CloneURL() = %q, want %q", got, want)
	}
}

func TestRepoRef_SameRepoIgnoresCaseAndTransport(t *testing.T) {
	a, _ := ParseRepoRef("https://github.com/BBorn/TaskYou.git")
	b, _ := ParseRepoRef("git@github.com:bborn/taskyou")
	if !a.SameRepo(b) {
		t.Errorf("%+v and %+v should be the same repo", a, b)
	}
	other, _ := ParseRepoRef("bborn/other")
	if a.SameRepo(other) {
		t.Errorf("%+v and %+v should not be the same repo", a, other)
	}
}

func TestRepoRef_StringQualifiesNonGitHubHosts(t *testing.T) {
	gh, _ := ParseRepoRef("bborn/taskyou")
	if got, want := gh.String(), "bborn/taskyou"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	gl, _ := ParseRepoRef("https://gitlab.com/bborn/taskyou")
	if got, want := gl.String(), "gitlab.com/bborn/taskyou"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// fakeCheckout makes dir look like a git checkout of remote.
func fakeCheckout(t *testing.T, dir, remote string, remotes map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	remotes[dir] = remote
}

func testCloner(t *testing.T, remotes map[string]string) (Cloner, string) {
	t.Helper()
	root := t.TempDir()
	return Cloner{
		Root: root,
		RemoteURL: func(dir string) (string, error) {
			if url, ok := remotes[dir]; ok {
				return url, nil
			}
			return "", errors.New("no origin remote")
		},
	}, root
}

func TestResolve_FreshDestination(t *testing.T) {
	c, root := testCloner(t, map[string]string{})
	ref, _ := ParseRepoRef("bborn/taskyou")

	dest, err := c.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(root, "taskyou"); dest.Path != want {
		t.Errorf("Path = %q, want %q", dest.Path, want)
	}
	if dest.Reuse || dest.Renamed {
		t.Errorf("fresh destination should be neither reused nor renamed: %+v", dest)
	}
}

func TestResolve_ExistingCloneOfSameRepoIsReused(t *testing.T) {
	remotes := map[string]string{}
	c, root := testCloner(t, remotes)
	ref, _ := ParseRepoRef("https://github.com/bborn/taskyou")
	existing := filepath.Join(root, "taskyou")
	// An ssh remote for the same repo still counts as the same checkout.
	fakeCheckout(t, existing, "git@github.com:bborn/taskyou.git", remotes)

	dest, err := c.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dest.Path != existing || !dest.Reuse {
		t.Errorf("Resolve = %+v, want reuse of %q", dest, existing)
	}
}

func TestResolve_UnrelatedDirectoryGetsNonCollidingName(t *testing.T) {
	remotes := map[string]string{}
	c, root := testCloner(t, remotes)
	ref, _ := ParseRepoRef("bborn/taskyou")

	// Something else entirely lives at ~/Projects/taskyou.
	if err := os.MkdirAll(filepath.Join(root, "taskyou", "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dest, err := c.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(root, "taskyou-2"); dest.Path != want {
		t.Errorf("Path = %q, want %q", dest.Path, want)
	}
	if !dest.Renamed || dest.Reuse {
		t.Errorf("Resolve = %+v, want renamed and not reused", dest)
	}
}

func TestResolve_SkipsPastUnrelatedDirsToAnExistingClone(t *testing.T) {
	remotes := map[string]string{}
	c, root := testCloner(t, remotes)
	ref, _ := ParseRepoRef("bborn/taskyou")

	if err := os.MkdirAll(filepath.Join(root, "taskyou", "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A checkout of a *different* repo also occupies taskyou-2.
	fakeCheckout(t, filepath.Join(root, "taskyou-2"), "https://github.com/someone/taskyou.git", remotes)
	fakeCheckout(t, filepath.Join(root, "taskyou-3"), "https://github.com/bborn/taskyou.git", remotes)

	dest, err := c.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(root, "taskyou-3"); dest.Path != want || !dest.Reuse {
		t.Errorf("Resolve = %+v, want reuse of %q", dest, want)
	}
}

func TestResolve_ExistingEmptyDirectoryIsUsedAsIs(t *testing.T) {
	c, root := testCloner(t, map[string]string{})
	ref, _ := ParseRepoRef("bborn/taskyou")
	empty := filepath.Join(root, "taskyou")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dest, err := c.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dest.Path != empty || dest.Renamed || dest.Reuse {
		t.Errorf("Resolve = %+v, want plain use of %q", dest, empty)
	}
}

func TestClone_UsesRefCloneURLAndDestination(t *testing.T) {
	root := t.TempDir()
	var gotURL, gotDest string
	c := Cloner{Root: root, Run: func(_ context.Context, url, dest string) error {
		gotURL, gotDest = url, dest
		return os.MkdirAll(filepath.Join(dest, ".git"), 0o755)
	}}
	ref, _ := ParseRepoRef("bborn/taskyou")
	dest := filepath.Join(root, "taskyou")

	if err := c.Clone(context.Background(), ref, dest); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if gotURL != "https://github.com/bborn/taskyou.git" {
		t.Errorf("clone url = %q", gotURL)
	}
	if gotDest != dest {
		t.Errorf("clone dest = %q, want %q", gotDest, dest)
	}
}

func TestClone_FailureLeavesNoPartialClone(t *testing.T) {
	root := t.TempDir()
	c := Cloner{Root: root, Run: func(_ context.Context, _, dest string) error {
		// Half a clone on disk, then a failure — exactly what git leaves.
		if err := os.MkdirAll(filepath.Join(dest, ".git", "objects"), 0o755); err != nil {
			return err
		}
		return &CloneError{Stderr: "remote: Repository not found.\nfatal: repository not found", Err: errors.New("exit 128")}
	}}
	ref, _ := ParseRepoRef("bborn/nope")
	dest := filepath.Join(root, "nope")

	err := c.Clone(context.Background(), ref, dest)
	if err == nil {
		t.Fatal("expected clone to fail")
	}
	if !strings.Contains(err.Error(), "Repository not found") {
		t.Errorf("error should surface git's stderr, got %q", err)
	}
	if pathExists(dest) {
		t.Errorf("%s should have been removed after a failed clone", dest)
	}
}

func TestClone_FailureRestoresAPreExistingEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "taskyou")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	c := Cloner{Root: root, Run: func(_ context.Context, _, d string) error {
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
			return err
		}
		return errors.New("boom")
	}}
	ref, _ := ParseRepoRef("bborn/taskyou")

	if err := c.Clone(context.Background(), ref, dest); err == nil {
		t.Fatal("expected clone to fail")
	}
	if !dirIsEmpty(dest) {
		t.Errorf("%s should be left empty, as it was found", dest)
	}
}

func TestClone_RefusesToWriteIntoANonEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "taskyou")
	if err := os.MkdirAll(filepath.Join(dest, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ran := false
	c := Cloner{Root: root, Run: func(_ context.Context, _, _ string) error { ran = true; return nil }}
	ref, _ := ParseRepoRef("bborn/taskyou")

	if err := c.Clone(context.Background(), ref, dest); err == nil {
		t.Fatal("expected an error for a non-empty destination")
	}
	if ran {
		t.Error("clone should not have run against a non-empty directory")
	}
	if !pathExists(filepath.Join(dest, "src")) {
		t.Error("existing contents must be left alone")
	}
}

func TestCloneErrorMessage_KeepsTheReason(t *testing.T) {
	stderr := "Cloning into '/home/u/Projects/taskyou'...\n" +
		"remote: Enumerating objects: 12, done.\n" +
		"remote: Repository not found.\n" +
		"fatal: repository 'https://github.com/bborn/nope.git/' not found\n"
	got := CloneErrorMessage(stderr)
	if strings.Contains(got, "Cloning into") || strings.Contains(got, "Enumerating") {
		t.Errorf("progress chatter should be dropped, got %q", got)
	}
	if !strings.Contains(got, "Repository not found") || !strings.Contains(got, "fatal:") {
		t.Errorf("the reason should survive, got %q", got)
	}
}

func TestLooksLikeRepoRef(t *testing.T) {
	for _, in := range []string{"https://github.com/bborn/taskyou", "git@github.com:bborn/taskyou.git", "bborn/taskyou", "github.com/x"} {
		if !LooksLikeRepoRef(in) {
			t.Errorf("LooksLikeRepoRef(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"", "taskyou", "my project"} {
		if LooksLikeRepoRef(in) {
			t.Errorf("LooksLikeRepoRef(%q) = true, want false", in)
		}
	}
}

func TestCloneErrorMessage_AddsTheNextStepForAuthFailures(t *testing.T) {
	got := CloneErrorMessage("fatal: could not read Username for 'https://github.com': terminal prompts disabled")
	if !strings.Contains(got, "could not read Username") {
		t.Errorf("git's own words should survive, got %q", got)
	}
	if !strings.Contains(got, "gh auth login") {
		t.Errorf("an auth failure should name the fix, got %q", got)
	}

	plain := CloneErrorMessage("fatal: unable to access 'https://github.com/o/r.git/': Could not resolve host: github.com")
	if strings.Contains(plain, "gh auth login") {
		t.Errorf("a network failure isn't an auth problem, got %q", plain)
	}
}
