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
//
// Branch selection mirrors the LOCAL setupWorktree so a downstream pipeline
// phase lands on the shared branch its predecessor pushed — not on a per-task
// branch cut from origin/main. A pipeline's sequential phase carries
// SourceBranch=<shared> with no BranchName of its own, and a fan-out peer
// carries BranchName=<stepBranch>, SourceBranch=<shared>; both must consult
// SourceBranch here exactly as addSourceBranchWorktree/addStepBranchWorktree do
// for a local placement. A carry-move (BranchName==SourceBranch) and a
// standalone task (SourceBranch=="") fall through to the legacy "own branch
// from origin/main" path the carry-move still relies on.
func (e *Executor) setupRemoteWorktree(ctx context.Context, task *db.Task, r RemoteRunner) (remoteWorktree, error) {
	repo := r.WorkDir
	if strings.TrimSpace(repo) == "" {
		return remoteWorktree{}, fmt.Errorf("placement named no checkout on %s", r.Host)
	}

	slug := slugify(task.Title, 40)
	dirName := fmt.Sprintf("%d-%s", task.ID, slug)

	branch, mode, source := remoteWorktreePlan(task, slug)

	ctx, cancel := context.WithTimeout(ctx, remoteWorktreeTimeout)
	defer cancel()

	// Every git call in the script names the repo explicitly, so the shell's own
	// working directory cannot change what it operates on.
	cmd := r.Command(ctx, repo, "sh", "-c", remoteWorktreeScript(repo, dirName, branch, mode, source))

	// The two streams are captured SEPARATELY. The script says what it did on
	// stdout and leaves git's chatter on stderr, but ssh delivers those as two
	// channels and os/exec copies them with two goroutines, so a merged capture
	// puts them in whatever order the copies happened to win — for a worktree
	// that provisions in well under a second, routinely the answer first and
	// "HEAD is now at ..." after it. Reading the answer out of a merged stream
	// therefore failed the FIRST run of every remotely placed task (the retry
	// then found the directory and reported "reused", which is why this looked
	// intermittent).
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	chatter := strings.TrimSpace(stderr.String())
	if err != nil {
		return remoteWorktree{}, fmt.Errorf("could not create a worktree on %s: %v (%s)", r.Host, err, chatter)
	}

	state, path, ok := parseRemoteWorktreeAnswer(stdout.String())
	if !ok {
		return remoteWorktree{}, fmt.Errorf("could not create a worktree on %s: unexpected output %q (%s)", r.Host, strings.TrimSpace(stdout.String()), chatter)
	}

	return remoteWorktree{Path: path, Branch: branch, Created: state == "created"}, nil
}

// parseRemoteWorktreeAnswer pulls the script's answer — "created <path>" or
// "reused <path>" — out of its stdout.
//
// It scans for the line rather than taking the last one: a login shell is free
// to print on stdout before the script runs (a ~/.profile that echoes, a
// version manager announcing itself), and none of that should read as a failure
// to provision.
func parseRemoteWorktreeAnswer(out string) (state, path string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		s, p, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found || (s != "created" && s != "reused") || strings.TrimSpace(p) == "" {
			continue
		}
		state, path, ok = s, strings.TrimSpace(p), true
	}
	return state, path, ok
}

// remoteWorktreeMode picks the form of worktree provisioning for a task.
type remoteWorktreeMode int

const (
	// modeFresh is the legacy provisioning used by carry-moves, standalone
	// tasks and pipeline roots: attach to $branch where it already exists
	// (locally, then on origin), and otherwise cut a fresh $branch from the
	// default branch. The carry-move's case 2 ("attach to origin/<branch>")
	// and a brand-new standalone task's case 3 ("cut from main") both live
	// here, and both depend on the fall-through to the default branch.
	modeFresh remoteWorktreeMode = iota
	// modeAttach is the remote analogue of addSourceBranchWorktree: a
	// sequential pipeline phase ATTACHES to the shared branch its predecessor
	// pushed, refusing to fall back to the default branch when neither a local
	// nor an origin ref exists. Falling back to main here silently drops the
	// predecessor's work (Plan's PLAN.md, the prior step's commits) — the
	// defect the local path's addSourceBranchWorktree was written to prevent.
	// With modeAttach, $branch IS the shared branch name.
	modeAttach
	// modeStepBranch is the remote analogue of addStepBranchWorktree: a
	// fan-out peer CUTS its own $stepBranch from origin/<source> (or local
	// <source> when origin has no such ref), with --no-track so a later bare
	// `git push` from the step cannot land its commits on the shared branch.
	// $branch here is the step's own name and $source is the shared branch.
	modeStepBranch
)

// remoteWorktreePlan derives the (branch, mode, source) tuple a task's
// provisioning script should run under. The same task shape behaves the same
// way whether it is placed locally (setupWorktree) or remotely
// (setupRemoteWorktree): a sequential phase attaches to its SourceBranch, a
// fan-out peer cuts its BranchName from its SourceBranch, and everything else
// (carry-move, standalone task, pipeline root) keeps the legacy "fresh branch
// from the default branch" path.
func remoteWorktreePlan(task *db.Task, slug string) (branch string, mode remoteWorktreeMode, source string) {
	switch {
	case strings.TrimSpace(task.SourceBranch) != "" && strings.TrimSpace(task.BranchName) == "":
		// Sequential downstream phase (Plan→Code, Code→Collect). The task has
		// no branch of its own and attaches to the shared branch its
		// predecessor pushed.
		return task.SourceBranch, modeAttach, task.SourceBranch
	case strings.TrimSpace(task.SourceBranch) != "" && task.BranchName != task.SourceBranch:
		// Fan-out peer: cuts its own step branch from the shared branch.
		return task.BranchName, modeStepBranch, task.SourceBranch
	default:
		// Carry-move (BranchName==SourceBranch), pipeline root or standalone
		// task (SourceBranch==""): unchanged legacy provisioning.
		return newWorktreeBranchName(task, slug), modeFresh, ""
	}
}

// remoteWorktreeScript renders the provisioning script.
//
// It deliberately does NOT branch from the checkout's current HEAD: a fleet
// host's primary clone is normally parked on whatever someone last worked on
// (task 5198's was on another task's branch), and cutting a task's branch from
// that silently inherits unrelated work. It prefers origin/HEAD, then
// origin/main, then origin/master, and only falls back to the current HEAD when
// the repo has no remote at all.
//
// mode selects how $branch is resolved against origin/local refs. modeFresh
// (the default for carry-moves, standalone tasks and pipeline roots) attaches
// when $branch exists anywhere and otherwise cuts it fresh from the default
// branch. modeAttach and modeStepBranch are the downstream-pipeline path: they
// consult $source (the shared branch a predecessor pushed) and refuse to fall
// back to main when the shared branch is missing — falling back here is
// exactly the silent-predecessor-loss bug the local path's
// addSourceBranchWorktree / addStepBranchWorktree exist to prevent.
func remoteWorktreeScript(repo, dirName, branch string, mode remoteWorktreeMode, source string) string {
	q := shellQuoteRemotePath
	lines := []string{
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
	}
	if mode == modeStepBranch {
		// The shared branch the predecessor pushed, named in git's refs so
		// the worktree add below can attach to it. Quoted for the same reason
		// $branch is — a pipeline branch name can contain slashes.
		lines = append(lines, "source="+shellQuote(source))
	}
	switch mode {
	case modeAttach:
		// A sequential downstream phase ATTACHES to the shared branch. The
		// predecessor pushed origin/<branch>, so the second case fires and
		// `worktree add -b <branch> <path> origin/<branch>` configures the
		// upstream to be origin/<branch> (not origin/main) — which is what
		// makes the step's later bare `git push` land its commits on the
		// shared branch instead of failing with a name-mismatch error.
		//
		// The case 3 fall-through to origin/main that exists in modeFresh is
		// deliberately absent here: when neither a local nor an origin ref for
		// the shared branch exists, the only honest answer is an error. A
		// fresh branch from main would provision cleanly, start cleanly, and
		// contain none of the predecessor's work — which is the bug the local
		// addSourceBranchWorktree was written to prevent.
		//
		// Reclaim the shared branch from a finished predecessor's worktree
		// before the case 1 attach. A prior step's worktree is not torn down
		// when it completes — kept for inspection — so it keeps the shared
		// branch checked out and a plain `worktree add "$wt" "$branch"`
		// against the existing local ref dies with "'<branch>' is already
		// checked out at ...". The local addSourceBranchWorktree handles this
		// with releaseBranchFromFinishedHolder, which detaches the holder's
		// HEAD only after a daemon check that the holder's task is finished.
		// The remote script has no daemon state, but every worktree ty ever
		// creates remotely lives under $repo/.task-worktrees/ (the script
		// itself puts nothing else there), and the spawn path is only reached
		// for a downstream phase after the workflow has advanced, which is
		// only after the predecessor's WorkflowStepFinished fired. So a
		// holder under $repo/.task-worktrees/ IS a finished prior step's
		// worktree by construction, and detaching it is exactly the safe
		// release the local path does for the same case. A holder anywhere
		// else is not ours to touch — fail loudly and let the daemon surface
		// the message rather than detach a worktree someone might be using.
		lines = append(lines, []string{
			`holder=$(git -C "$repo" worktree list --porcelain | awk -v b="refs/heads/$branch" '/^worktree / { wt = substr($0, 10) } /^branch / { if ($2 == b) print wt }')`,
			`if [ -n "$holder" ]; then`,
			`  case "$holder" in "$repo"/.task-worktrees/*)`,
			`    git -C "$holder" checkout --detach >&2`,
			`    ;;`,
			`  *)`,
			`    echo "shared branch $branch is held by $holder, which is not a ty worktree; refusing to detach" >&2`,
			`    exit 1`,
			`  ;;`,
			`  esac`,
			`fi`,
			`if git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then`,
			// Fast-forward a strictly-behind local ref to origin/<branch> before
			// attaching, mirroring addSourceBranchWorktree (executor.go:6421-6429).
			// A host that ran an earlier phase carries refs/heads/<branch> at the
			// earlier phase's tip; `git fetch` advances only refs/remotes/origin/*
			// and leaves refs/heads/* untouched, so attaching to the local ref
			// verbatim would start the next phase from a stale baseline. The
			// strict-ancestor pair guards against divergence: only when the local
			// ref is an ancestor of origin/<branch> AND the reverse is not true
			// (i.e. strictly behind, not equal and not ahead/diverged) is the ref
			// moved — so a local ref that carries this workflow's own unpushed
			// commits is never force-moved over them.
			`  if git -C "$repo" show-ref --verify --quiet "refs/remotes/origin/$branch" &&`,
			`     git -C "$repo" merge-base --is-ancestor "refs/heads/$branch" "refs/remotes/origin/$branch" &&`,
			`     ! git -C "$repo" merge-base --is-ancestor "refs/remotes/origin/$branch" "refs/heads/$branch"; then`,
			`    git -C "$repo" update-ref "refs/heads/$branch" "refs/remotes/origin/$branch" >&2`,
			`  fi`,
			`  git -C "$repo" worktree add "$wt" "$branch" >&2`,
			`elif git -C "$repo" show-ref --verify --quiet "refs/remotes/origin/$branch"; then`,
			`  git -C "$repo" worktree add -b "$branch" "$wt" "origin/$branch" >&2`,
			`else`,
			`  echo "shared branch $branch not found on origin or locally; refusing to provision from the default branch" >&2`,
			`  exit 1`,
			`fi`,
		}...)
	case modeStepBranch:
		// A fan-out peer CUTS its own $branch from the shared $source branch.
		// --no-track mirrors the local addStepBranchWorktree: with tracking,
		// `worktree add -b $branch <path> origin/$source` would set $branch's
		// upstream to the SHARED branch, so a later bare `git push` from the
		// step would push its commits onto the branch its instructions
		// explicitly tell it not to touch.
		//
		// The first case (refs/heads/$branch) is a retry of a peer that
		// already ran: keep its own branch with whatever it committed before,
		// exactly like addStepBranchWorktree does locally.
		//
		// A missing $source (neither on origin nor locally) is an error, not
		// a fall-through to main: cutting from main would start the peer from
		// the baseline with none of the predecessor's work, the same
		// silent-drop defect modeAttach guards against.
		lines = append(lines, []string{
			`if git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then`,
			`  git -C "$repo" worktree add "$wt" "$branch" >&2`,
			`elif git -C "$repo" show-ref --verify --quiet "refs/remotes/origin/$source"; then`,
			`  git -C "$repo" worktree add --no-track -b "$branch" "$wt" "origin/$source" >&2`,
			`elif git -C "$repo" show-ref --verify --quiet "refs/heads/$source"; then`,
			`  git -C "$repo" worktree add --no-track -b "$branch" "$wt" "$source" >&2`,
			`else`,
			`  echo "shared branch $source not found on origin or locally; refusing to provision from the default branch" >&2`,
			`  exit 1`,
			`fi`,
		}...)
	default:
		// modeFresh — the original behavior the carry-move / standalone task /
		// pipeline root still rely on. Compute the default-branch $base, then
		// attach to a local $branch, then to origin/$branch, and finally cut a
		// fresh $branch from $base.
		//
		// origin/$branch is the SECOND case and it is the one a move depends
		// on. A task carried onto this host has its work on origin and no
		// local ref for it, so a check for refs/heads alone falls through to
		// "cut a new branch from main" — which provisions cleanly, starts
		// cleanly, and contains none of the work the carry gate had just
		// finished proving was safe. Attaching to origin/$branch is what makes
		// step 6 of the move design ("the next spawn clones the pushed
		// branch") actually true.
		lines = append(lines, []string{
			`base=""`,
			`for c in "$(git -C "$repo" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null)" origin/main origin/master; do`,
			`  [ -n "$c" ] || continue`,
			`  if git -C "$repo" rev-parse --verify --quiet "$c" >/dev/null 2>&1; then base="$c"; break; fi`,
			`done`,
			`[ -n "$base" ] || base=$(git -C "$repo" rev-parse --abbrev-ref HEAD)`,
			`if git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then`,
			`  git -C "$repo" worktree add "$wt" "$branch" >&2`,
			`elif git -C "$repo" show-ref --verify --quiet "refs/remotes/origin/$branch"; then`,
			`  git -C "$repo" worktree add -b "$branch" "$wt" "origin/$branch" >&2`,
			`else`,
			`  git -C "$repo" worktree add -b "$branch" "$wt" "$base" >&2`,
			`fi`,
		}...)
	}
	lines = append(lines, `echo "created $wt"`)
	return strings.Join(lines, "\n")
}
