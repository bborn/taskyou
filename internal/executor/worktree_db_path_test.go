package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// BuildCommand must propagate WORKTREE_DB_PATH into the agent's env when the daemon
// runs against a non-default DB (an isolated instance). Without it, the taskyou
// mcp-server the agent spawns opens the DEFAULT (live) DB instead of the isolated
// one, so taskyou_* tools operate on the wrong board.
func TestBuildCommandPropagatesWorktreeDBPath(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "plain", Path: "/tmp/plain"}); err != nil {
		t.Fatal(err)
	}
	ex := New(database, &config.Config{})
	claude := ex.executorFactory.Get(db.ExecutorClaude)
	task := &db.Task{ID: 7, Project: "plain", Port: 8080, WorktreePath: "/tmp/plain/.wt/7"}

	// Isolated instance: WORKTREE_DB_PATH set -> command must carry it, before `claude`.
	t.Setenv("WORKTREE_DB_PATH", "/tmp/iso/tasks.db")
	got := claude.BuildCommand(task, "", "")
	if !strings.Contains(got, `WORKTREE_DB_PATH="/tmp/iso/tasks.db"`) {
		t.Errorf("BuildCommand must carry WORKTREE_DB_PATH; got:\n  %s", got)
	}
	if di, pi := strings.Index(got, "WORKTREE_DB_PATH="), strings.Index(got, "claude "); di < 0 || pi < 0 || di > pi {
		t.Errorf("WORKTREE_DB_PATH must precede `claude`; got:\n  %s", got)
	}

	// Default instance: unset -> no WORKTREE_DB_PATH in the command.
	t.Setenv("WORKTREE_DB_PATH", "")
	if def := claude.BuildCommand(task, "", ""); strings.Contains(def, "WORKTREE_DB_PATH=") {
		t.Errorf("default instance: BuildCommand must NOT set WORKTREE_DB_PATH; got:\n  %s", def)
	}
}
