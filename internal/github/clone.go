package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultHost is the host assumed for shorthand refs ("owner/repo").
const DefaultHost = "github.com"

// RepoRef identifies a repository on a git host. It is the normalized form of
// every URL shape a user might paste (https, scp-style ssh, or shorthand).
type RepoRef struct {
	Host  string // e.g. "github.com"
	Owner string
	Name  string
	// SSH records that the user pasted an ssh URL. We clone back over ssh so
	// their key-based auth keeps working for private repos.
	SSH bool
}

// Slug is the "owner/repo" form used in prose and UI copy.
func (r RepoRef) Slug() string { return r.Owner + "/" + r.Name }

// String is the slug, qualified with the host when it isn't github.com.
func (r RepoRef) String() string {
	if !strings.EqualFold(r.Host, DefaultHost) {
		return r.Host + "/" + r.Slug()
	}
	return r.Slug()
}

// CloneURL is the URL handed to `git clone`.
func (r RepoRef) CloneURL() string {
	if r.SSH {
		return fmt.Sprintf("git@%s:%s/%s.git", r.Host, r.Owner, r.Name)
	}
	return fmt.Sprintf("https://%s/%s/%s.git", r.Host, r.Owner, r.Name)
}

// SameRepo reports whether two refs point at the same repository. Host, owner
// and name are compared case-insensitively; the transport is ignored, so an
// https clone matches an ssh remote.
func (r RepoRef) SameRepo(other RepoRef) bool {
	return strings.EqualFold(r.Host, other.Host) &&
		strings.EqualFold(r.Owner, other.Owner) &&
		strings.EqualFold(r.Name, other.Name)
}

var (
	// GitHub owners are alphanumeric plus hyphens; other hosts are looser, so
	// dots and underscores are tolerated. Anything else is rejected rather
	// than handed to git.
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	namePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hostPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}(:[0-9]+)?$`)
	scpPattern   = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+):(.+)$`)
)

// ParseRepoRef normalizes the repo URL forms people actually paste:
//
//	https://github.com/owner/repo        (with or without .git or a trailing /)
//	git@github.com:owner/repo.git
//	ssh://git@github.com/owner/repo.git
//	github.com/owner/repo
//	owner/repo
//
// Anything else returns an error whose text names the next step, so it can be
// shown inline. Nothing is shelled out to git before this succeeds.
func ParseRepoRef(input string) (RepoRef, error) {
	s := strings.TrimSpace(input)
	s = strings.Trim(s, "<>\"'")
	if s == "" {
		return RepoRef{}, errors.New("enter a repo URL, e.g. https://github.com/owner/repo")
	}

	host := DefaultHost
	ssh := false
	var path string

	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return RepoRef{}, fmt.Errorf("that doesn't look like a repo URL — try https://github.com/owner/repo")
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "http":
		case "ssh", "git":
			ssh = true
		default:
			return RepoRef{}, fmt.Errorf("%s:// URLs aren't supported — try https://github.com/owner/repo", u.Scheme)
		}
		host = u.Host
		path = u.Path
	case scpPattern.MatchString(s) && strings.Contains(s, "@"):
		m := scpPattern.FindStringSubmatch(s)
		host, path, ssh = m[1], m[2], true
	default:
		// Shorthand: "owner/repo", or "github.com/owner/repo".
		segments := strings.Split(strings.Trim(s, "/"), "/")
		if len(segments) > 2 && hostPattern.MatchString(segments[0]) {
			host = segments[0]
			path = strings.Join(segments[1:], "/")
		} else {
			path = s
		}
	}

	if !hostPattern.MatchString(host) {
		return RepoRef{}, fmt.Errorf("%q isn't a host TaskYou can clone from — try https://github.com/owner/repo", host)
	}

	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) > 2 {
		// A link to a file, branch or issue inside a repo. Name the repo part
		// so the fix is one edit away.
		return RepoRef{}, fmt.Errorf("that URL points inside a repo — use just the repo: %s/%s",
			segments[0], strings.TrimSuffix(segments[1], ".git"))
	}
	if len(segments) != 2 {
		return RepoRef{}, errors.New("a repo needs an owner and a name, e.g. https://github.com/owner/repo")
	}

	owner := segments[0]
	name := strings.TrimSuffix(segments[1], ".git")
	if !ownerPattern.MatchString(owner) || !namePattern.MatchString(name) {
		return RepoRef{}, errors.New("that doesn't look like a repo URL — try https://github.com/owner/repo")
	}

	return RepoRef{Host: strings.ToLower(host), Owner: owner, Name: name, SSH: ssh}, nil
}

// LooksLikeRepoRef reports whether input is shaped like a repo URL at all —
// used to decide whether a parse failure is worth an inline error (a paste
// gone wrong) or just an ordinary search term.
func LooksLikeRepoRef(input string) bool {
	s := strings.TrimSpace(input)
	if s == "" {
		return false
	}
	return strings.Contains(s, "://") ||
		strings.Contains(s, "@") ||
		strings.Contains(s, "github.com") ||
		strings.Count(strings.Trim(s, "/"), "/") >= 1
}

// CloneDestination is where a clone will land, and how we got there.
type CloneDestination struct {
	Path string
	// Reuse is true when Path already holds a checkout of the same repo, so
	// there's nothing to clone — the caller can adopt it as-is.
	Reuse bool
	// Renamed is true when the natural directory name was taken by something
	// else and a non-colliding one was picked instead.
	Renamed bool
}

// CommandBuilder builds a command to run in workDir. It is the signature of
// executor.Runner's Command method, restated here because internal/executor
// imports this package and the dependency can only point one way.
//
// A clone has to happen wherever the checkout is going to live, so the git
// binary is invoked through one of these rather than directly. Nil means the
// local machine, which is what it has always been.
type CommandBuilder func(ctx context.Context, workDir, name string, args ...string) *exec.Cmd

// Cloner clones repos into a root directory. The zero value clones into
// ~/Projects using the git binary on this machine; tests substitute the seams.
type Cloner struct {
	// Root is where clones land. Empty means ~/Projects.
	Root string
	// RemoteURL returns dir's origin remote. Nil means ask git.
	RemoteURL func(dir string) (string, error)
	// Run performs the clone itself. Nil means run `git clone`.
	Run func(ctx context.Context, cloneURL, dest string) error
	// Command builds the git invocations. Nil means run them locally.
	Command CommandBuilder
}

// command returns the builder to use, defaulting to local execution.
func (c Cloner) command() CommandBuilder {
	if c.Command != nil {
		return c.Command
	}
	return localCommand
}

// localCommand is the default CommandBuilder: a plain local command.
func localCommand(ctx context.Context, workDir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// DefaultCloneRoot is where clones land unless told otherwise: ~/Projects.
func DefaultCloneRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "Projects"
	}
	return filepath.Join(home, "Projects")
}

func (c Cloner) root() string {
	if c.Root != "" {
		return c.Root
	}
	return DefaultCloneRoot()
}

func (c Cloner) remoteURL(dir string) (string, error) {
	if c.RemoteURL != nil {
		return c.RemoteURL(dir)
	}
	return gitOriginURL(c.command(), dir)
}

// maxDestinationAttempts bounds the "repo-2, repo-3, …" search so a wedged
// directory tree can't spin forever.
const maxDestinationAttempts = 50

// Resolve picks the directory ref should be cloned into. An existing checkout
// of the same repo is reused; an unrelated directory of the same name is
// stepped around ("repo-2"); an existing empty directory is used as-is,
// because git clones into those happily.
func (c Cloner) Resolve(ref RepoRef) (CloneDestination, error) {
	base := filepath.Join(c.root(), ref.Name)
	for i := 1; i <= maxDestinationAttempts; i++ {
		candidate := base
		if i > 1 {
			candidate = base + "-" + strconv.Itoa(i)
		}
		switch {
		case !pathExists(candidate) || dirIsEmpty(candidate):
			return CloneDestination{Path: candidate, Renamed: i > 1}, nil
		case c.IsCheckoutOf(candidate, ref):
			return CloneDestination{Path: candidate, Reuse: true, Renamed: i > 1}, nil
		}
	}
	return CloneDestination{}, fmt.Errorf("no free directory near %s — clone somewhere else", base)
}

// IsCheckoutOf reports whether dir is a git checkout whose origin is ref.
func (c Cloner) IsCheckoutOf(dir string, ref RepoRef) bool {
	if !pathExists(filepath.Join(dir, ".git")) {
		return false
	}
	remote, err := c.remoteURL(dir)
	if err != nil || strings.TrimSpace(remote) == "" {
		return false
	}
	parsed, err := ParseRepoRef(remote)
	if err != nil {
		return false
	}
	return parsed.SameRepo(ref)
}

// CloneError carries git's own stderr so the UI can show the user exactly what
// git said (bad auth, no such repo, no network).
type CloneError struct {
	Stderr string
	Err    error
}

func (e *CloneError) Error() string {
	if msg := CloneErrorMessage(e.Stderr); msg != "" {
		return msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "clone failed"
}

func (e *CloneError) Unwrap() error { return e.Err }

// CloneErrorMessage distills git's stderr to the lines worth showing: progress
// chatter and the "Cloning into…" banner are dropped, the rest is kept verbatim.
func CloneErrorMessage(stderr string) string {
	noise := []string{
		"Cloning into",
		"Receiving objects",
		"Resolving deltas",
		"Updating files",
		"remote: Enumerating",
		"remote: Counting",
		"remote: Compressing",
		"remote: Total",
	}
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		skip := false
		for _, prefix := range noise {
			if strings.HasPrefix(line, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	// The last few lines carry the actual reason; earlier ones are context.
	if len(kept) > 3 {
		kept = kept[len(kept)-3:]
	}
	if hint := authHint(stderr); hint != "" {
		kept = append(kept, hint)
	}
	return strings.Join(kept, "\n")
}

// authHint spells out the next step when git couldn't get at the repo. Clones
// run with prompts disabled (a git waiting on a password looks like a hung
// TUI), so "could not read Username" means "no credentials here", and a
// missing repo is as often private as it is mistyped.
func authHint(stderr string) string {
	s := strings.ToLower(stderr)
	for _, marker := range []string{
		"terminal prompts disabled",
		"could not read username",
		"authentication failed",
		"repository not found",
		"permission denied",
	} {
		if strings.Contains(s, marker) {
			return "If the repo is private, sign in first (gh auth login), or paste the git@github.com: URL to use your ssh key."
		}
	}
	return ""
}

// Clone clones ref into dest. A failed or cancelled clone leaves nothing
// behind: whatever git managed to write is removed, and a directory that
// existed (empty) beforehand is restored empty.
func (c Cloner) Clone(ctx context.Context, ref RepoRef, dest string) error {
	if dest == "" {
		return errors.New("no destination to clone into")
	}
	preExisted := pathExists(dest)
	if preExisted && !dirIsEmpty(dest) {
		return fmt.Errorf("%s already exists and isn't empty — pick another destination", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", filepath.Dir(dest), err)
	}

	run := c.Run
	if run == nil {
		build := c.command()
		run = func(ctx context.Context, cloneURL, dest string) error {
			return gitClone(ctx, build, cloneURL, dest)
		}
	}
	if err := run(ctx, ref.CloneURL(), dest); err != nil {
		cleanupPartialClone(dest, preExisted)
		return err
	}
	return nil
}

// cleanupPartialClone removes a half-written clone. If the destination existed
// (and was empty) before we started, it is left behind empty as we found it.
func cleanupPartialClone(dest string, preExisted bool) {
	if err := os.RemoveAll(dest); err != nil {
		return
	}
	if preExisted {
		_ = os.MkdirAll(dest, 0o755)
	}
}

// gitClone is the real clone: `git clone <url> <dest>`, with stderr captured.
func gitClone(ctx context.Context, build CommandBuilder, cloneURL, dest string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git not found — install git, then try again")
	}
	var stderr strings.Builder
	cmd := build(ctx, "", "git", "clone", cloneURL, dest)
	cmd.Stderr = &stderr
	// Never let git stop for a credentials prompt: there's no terminal to
	// answer it, and a hung clone looks like a hung TUI.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &CloneError{Stderr: stderr.String(), Err: err}
	}
	return nil
}

// gitOriginURL reads dir's origin remote.
func gitOriginURL(build CommandBuilder, dir string) (string, error) {
	out, err := build(context.Background(), dir, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// dirIsEmpty reports whether p is a directory with no entries at all.
func dirIsEmpty(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) == 0
}
