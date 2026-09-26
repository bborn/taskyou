package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// fakeRemoteTmux stands in for ssh plus the tmux server on each placed host.
// The ssh stub runs the command it is given with a fake tmux first on PATH; that
// tmux keeps its windows in a file, one "host session window_id window_name"
// line per window, so a test can see exactly which windows a sweep ended. The
// host "asleep" is unreachable: ssh exits 255 before running anything.
type fakeRemoteTmux struct {
	state string // the windows file
	log   string // one line per ssh call: host, then the remote command
}

func newFakeRemoteTmux(t *testing.T, windows ...string) *fakeRemoteTmux {
	t.Helper()
	dir := t.TempDir()
	f := &fakeRemoteTmux{state: filepath.Join(dir, "windows"), log: filepath.Join(dir, "ssh.log")}
	if err := os.WriteFile(f.state, []byte(strings.Join(windows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tmux := `#!/bin/sh
cmd=$1; shift
target=
while [ $# -gt 0 ]; do
  case "$1" in -t) target=$2; shift ;; esac
  shift
done
case "$cmd" in
list-windows)
  out=$(awk -v h="$FAKE_HOST" -v s="${target#=}" '$1==h && $2==s {print $3, $4}' "$FAKE_TMUX_STATE")
  [ -n "$out" ] || { echo "can't find session: ${target#=}" >&2; exit 1; }
  printf '%s\n' "$out" ;;
kill-window)
  awk -v h="$FAKE_HOST" -v i="$target" '$1==h && $3==i {found=1} END {exit !found}' "$FAKE_TMUX_STATE" || exit 1
  awk -v h="$FAKE_HOST" -v i="$target" '!($1==h && $3==i)' "$FAKE_TMUX_STATE" > "$FAKE_TMUX_STATE.new"
  mv "$FAKE_TMUX_STATE.new" "$FAKE_TMUX_STATE" ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(tmux), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_TMUX_STATE", f.state)
	stubSSH(t, `#!/bin/sh
while [ "$1" != "--" ]; do shift; done
host=$2
printf '%s %s\n' "$host" "$3" >> `+shellQuote(f.log)+`
[ "$host" = asleep ] && exit 255
inner=${3#sh -lc }
FAKE_HOST=$host PATH=`+shellQuote(bin)+`:$PATH eval "sh -c $inner"
`)
	return f
}

func (f *fakeRemoteTmux) windows(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.state)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return lines
}

func (f *fakeRemoteTmux) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// placedTask creates a task that ran on host and leaves it in status, the way
// runRemoteSession and a human leave it.
func placedTask(t *testing.T, database *db.DB, host, status string) *db.Task {
	t.Helper()
	task := placementTestTask(t, database)
	if _, err := database.BeginRemoteRun(task.ID, host); err != nil {
		t.Fatal(err)
	}
	steps := []string{db.StatusProcessing}
	if status != db.StatusProcessing {
		steps = append(steps, db.StatusBlocked)
	}
	if status != db.StatusProcessing && status != db.StatusBlocked {
		steps = append(steps, status)
	}
	for _, to := range steps {
		if err := database.SetTaskStatus(task.ID, to, db.ActorCLI, "test setup",
			db.ByHuman("moved task #%d to %s", task.ID, to)); err != nil {
			t.Fatalf("set #%d %s: %v", task.ID, to, err)
		}
	}
	return task
}

// A task that is done or archived has an agent with nothing left to do. When
// the task ran on another host, its tmux windows there must be ended — or the
// agent keeps running (and keeps a pooled server busy) for days. A blocked task
// is waiting for a reply and must be left alone, as must windows that belong to
// another coordinator or to the host's own ty.
func TestFinishedRemoteTaskSessionsAreEnded(t *testing.T) {
	e, database := placementExecutor(t, t.TempDir())
	coordinator, err := database.CoordinatorID()
	if err != nil {
		t.Fatal(err)
	}
	session := "task-daemon-remote-" + coordinator

	done := placedTask(t, database, "mona", db.StatusDone)
	archived := placedTask(t, database, "mona", db.StatusArchived)
	blocked := placedTask(t, database, "mona", db.StatusBlocked)
	running := placedTask(t, database, "mona", db.StatusProcessing)
	asleep := placedTask(t, database, "asleep", db.StatusDone)

	w := func(host, sess, id string, taskID int64, suffix string) string {
		return fmt.Sprintf("%s %s %s task-%d%s", host, sess, id, taskID, suffix)
	}
	keep := []string{
		w("mona", session, "@4", blocked.ID, ""),
		w("mona", session, "@5", blocked.ID, "-shell"),
		w("mona", session, "@6", running.ID, ""),
		// Same task ID, but another coordinator's session and the host's own ty.
		w("mona", "task-daemon-remote-someoneelse", "@7", done.ID, ""),
		w("mona", "task-daemon-4242", "@8", done.ID, ""),
		w("asleep", session, "@1", asleep.ID, ""),
	}
	fake := newFakeRemoteTmux(t, append([]string{
		w("mona", session, "@1", done.ID, ""),
		w("mona", session, "@2", done.ID, "-shell"),
		w("mona", session, "@3", archived.ID, ""),
	}, keep...)...)

	now := time.Now()
	e.endFinishedRemoteSessions(context.Background(), now)

	sort.Strings(keep)
	if got := fake.windows(t); strings.Join(got, "\n") != strings.Join(keep, "\n") {
		t.Fatalf("windows after sweep:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(keep, "\n"))
	}
	for _, call := range fake.calls(t) {
		if strings.Contains(call, "rm ") || strings.Contains(call, "git ") {
			t.Errorf("ending a session must only end processes, never touch files: %s", call)
		}
	}
	logs, _ := database.GetTaskLogs(done.ID, 50)
	var logged bool
	for _, l := range logs {
		logged = logged || strings.Contains(l.Content, "mona")
	}
	if !logged {
		t.Error("the done task's log does not say its session on mona was ended")
	}

	// The ended runs are forgotten; the unreachable host's is kept for a retry.
	pending, err := database.FinishedRemoteRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].TaskID != asleep.ID {
		t.Fatalf("pending runs = %+v, want only task #%d on the unreachable host", pending, asleep.ID)
	}

	// The next sweep asks nothing of mona, and backs off the unreachable host...
	before := len(fake.calls(t))
	e.endFinishedRemoteSessions(context.Background(), now.Add(time.Second))
	if after := fake.calls(t); len(after) != before {
		t.Fatalf("second sweep made ssh calls: %v", after[before:])
	}

	// ...until its backoff has passed, when it is tried again.
	e.endFinishedRemoteSessions(context.Background(), now.Add(remoteSessionEndMaxBackoff))
	after := fake.calls(t)
	if len(after) != before+1 || !strings.HasPrefix(after[len(after)-1], "asleep ") {
		t.Fatalf("unreachable host was not retried after its backoff: %v", after[before:])
	}
}

// An unreachable host must never stand in the way of the status change: the
// status is the database's, and the session is ended later by the daemon.
func TestClosingRemoteTaskDoesNotNeedItsHost(t *testing.T) {
	_, database := placementExecutor(t, t.TempDir())
	fake := newFakeRemoteTmux(t)
	task := placedTask(t, database, "asleep", db.StatusBlocked)
	if err := database.SetTaskStatus(task.ID, db.StatusDone, db.ActorCLI, "closed",
		db.ByHuman("closed task #%d", task.ID)); err != nil {
		t.Fatalf("closing a task whose host is unreachable: %v", err)
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Fatalf("closing a task contacted its host: %v", calls)
	}
}
