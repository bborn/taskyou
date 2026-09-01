package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// remoteWorktree is the isolated workspace a remotely placed task was given on
// its host: a git worktree of that host's checkout, on the task's own branch.
type remoteWorktree struct {
	// Path is the worktree directory ON THE PLACED HOST.
	Path string
	// Branch is the branch checked out in it.
	Branch string
	// Created is true when this run provisioned it, false when it was already
	// there from an earlier attempt.
	Created bool
}

// remoteWorktreeTimeout bounds the provisioning round trip. A first worktree in
// a large repo copies a checkout, so this is generous compared with the probes.
const remoteWorktreeTimeout = 5 * time.Minute

// setupRemoteWorktree gives a remotely placed task the same isolation a local
// one gets: its own git worktree, on its own branch, inside the checkout the
// placement handler named.
//
// Without this a remote run executes IN that checkout — which is the host's
// primary clone, routinely sitting on someone else's branch. The local path has
// refused to do that for years ("never fall back to project directory to prevent
// Claude from accidentally writing to the main repo"); the remote path skipped
// worktree setup entirely and did exactly what the local path forbids. Remote
// execution is not safe to enable without this.
//
// The whole thing is one idempotent shell script, run once over ssh, because
// every extra round trip is a second of latency and another way for the sequence
// to half-succeed. Re-running it for the same task returns the existing worktree
// rather than failing, which is what makes retries and resumes cheap.
func (e *Executor) setupRemoteWorktree(ctx context.Context, task *db.Task, r RemoteRunner) (remoteWorktree, error) {
	repo := r.WorkDir
	if strings.TrimSpace(repo) == "" {
		return remoteWorktree{}, fmt.Errorf("placement named no checkout on %s", r.Host)
	}

	slug := slugify(task.Title, 40)
	branch := newWorktreeBranchName(task, slug)
	dirName := fmt.Sprintf("%d-%s", task.ID, slug)

	ctx, cancel := context.WithTimeout(ctx, remoteWorktreeTimeout)
	defer cancel()

	// Every git call in the script names the repo explicitly, so the shell's own
	// working directory cannot change what it operates on.
	cmd := r.Command(ctx, repo, "sh", "-c", remoteWorktreeScript(repo, dirName, branch))
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return remoteWorktree{}, fmt.Errorf("could not create a worktree on %s: %v (%s)", r.Host, err, text)
	}

	// The script's last line is "created <path>" or "reused <path>"; anything
	// before it is git's own chatter, which we keep for the log.
	lines := strings.Split(text, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	state, path, ok := strings.Cut(last, " ")
	if !ok || (state != "created" && state != "reused") {
		return remoteWorktree{}, fmt.Errorf("could not create a worktree on %s: unexpected output %q", r.Host, text)
	}

	return remoteWorktree{Path: path, Branch: branch, Created: state == "created"}, nil
}

// remoteWorktreeScript renders the provisioning script.
//
// It deliberately does NOT branch from the checkout's current HEAD: a fleet
// host's primary clone is normally parked on whatever someone last worked on
// (task 5198's was on another task's branch), and cutting a task's branch from
// that silently inherits unrelated work. It prefers origin/HEAD, then
// origin/main, then origin/master, and only falls back to the current HEAD when
// the repo has no remote at all.
func remoteWorktreeScript(repo, dirName, branch string) string {
	q := shellQuoteRemotePath
	return strings.Join([]string{
		"set -e",
		"repo=" + q(repo),
		"branch=" + shellQuote(branch),
		"wt=\"$repo/.task-worktrees/" + dirName + "\"",
		`git -C "$repo" rev-parse --git-dir >/dev/null 2>&1 || { echo "not a git repository: $repo" >&2; exit 1; }`,
		// Keep the worktrees out of the host checkout's status without editing a
		// tracked .gitignore that belongs to the project, not to ty.
		`common=$(git -C "$repo" rev-parse --git-common-dir)`,
		`case "$common" in /*) ;; *) common="$repo/$common";; esac`,
		`grep -qxF '.task-worktrees/' "$common/info/exclude" 2>/dev/null || echo '.task-worktrees/' >> "$common/info/exclude" 2>/dev/null || true`,
		`mkdir -p "$repo/.task-worktrees"`,
		// Already provisioned by an earlier attempt: reuse it, so retries and
		// resumes land in the same directory with the same history.
		`if [ -d "$wt" ]; then echo "reused $wt"; exit 0; fi`,
		`git -C "$repo" fetch origin --quiet 2>/dev/null || true`,
		`base=""`,
		`for c in "$(git -C "$repo" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null)" origin/main origin/master; do`,
		`  [ -n "$c" ] || continue`,
		`  if git -C "$repo" rev-parse --verify --quiet "$c" >/dev/null 2>&1; then base="$c"; break; fi`,
		`done`,
		`[ -n "$base" ] || base=$(git -C "$repo" rev-parse --abbrev-ref HEAD)`,
		// An existing branch is checked out rather than recreated: that is a task
		// that ran here before and whose branch outlived its worktree.
		`if git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then`,
		`  git -C "$repo" worktree add "$wt" "$branch" >&2`,
		`else`,
		`  git -C "$repo" worktree add -b "$branch" "$wt" "$base" >&2`,
		`fi`,
		`echo "created $wt"`,
	}, "\n")
}
