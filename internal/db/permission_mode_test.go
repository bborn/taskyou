package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePermissionMode(t *testing.T) {
	cases := map[string]string{
		"auto":         PermissionModeAuto,        // Claude Code's auto mode
		"  Auto  ":     PermissionModeAuto,        // trimmed + case-insensitive
		"accept-edits": PermissionModeAcceptEdits, // canonical acceptEdits value
		"accept_edits": PermissionModeAcceptEdits,
		"acceptEdits":  PermissionModeAcceptEdits, // Claude Code's own spelling
		"dangerous":    PermissionModeDangerous,
		"default":      PermissionModeDefault,
		"prompt":       PermissionModeDefault,
		"":             "",
		"bogus":        "",
	}
	for in, want := range cases {
		if got := NormalizePermissionMode(in); got != want {
			t.Errorf("NormalizePermissionMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskEffectivePermissionMode(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want string
	}{
		{"explicit auto", Task{PermissionMode: PermissionModeAuto}, PermissionModeAuto},
		{"explicit accept-edits", Task{PermissionMode: PermissionModeAcceptEdits}, PermissionModeAcceptEdits},
		{"explicit dangerous", Task{PermissionMode: PermissionModeDangerous}, PermissionModeDangerous},
		{"explicit default", Task{PermissionMode: PermissionModeDefault}, PermissionModeDefault},
		{"legacy dangerous bool", Task{DangerousMode: true}, PermissionModeDangerous},
		{"empty falls back to default", Task{}, PermissionModeDefault},
		{"explicit wins over bool", Task{PermissionMode: PermissionModeAuto, DangerousMode: true}, PermissionModeAuto},
	}
	for _, c := range cases {
		if got := c.task.EffectivePermissionMode(); got != c.want {
			t.Errorf("%s: EffectivePermissionMode() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNextPermissionMode(t *testing.T) {
	cases := map[string]string{
		PermissionModeDefault:     PermissionModeAcceptEdits,
		PermissionModeAcceptEdits: PermissionModeAuto,
		PermissionModeAuto:        PermissionModeDangerous,
		PermissionModeDangerous:   PermissionModeDefault, // wraps around
		"":                        PermissionModeAcceptEdits,
		"bogus":                   PermissionModeAcceptEdits,
	}
	for in, want := range cases {
		if got := NextPermissionMode(in); got != want {
			t.Errorf("NextPermissionMode(%q) = %q, want %q", in, got, want)
		}
	}

	// Cycling four times returns to the start, covering all modes exactly once.
	seen := map[string]bool{}
	mode := PermissionModeDefault
	for i := 0; i < len(PermissionModeCycle); i++ {
		seen[mode] = true
		mode = NextPermissionMode(mode)
	}
	if mode != PermissionModeDefault {
		t.Errorf("cycle did not return to start, ended at %q", mode)
	}
	if len(seen) != 4 {
		t.Errorf("cycle did not visit all 4 modes, saw %d: %v", len(seen), seen)
	}
}

func TestPermissionModeLabel(t *testing.T) {
	cases := map[string]string{
		PermissionModeDefault:     "Prompt",
		PermissionModeAcceptEdits: "Accept-edits",
		PermissionModeAuto:        "Auto",
		PermissionModeDangerous:   "Dangerous",
		"":                        "Prompt",
	}
	for in, want := range cases {
		if got := PermissionModeLabel(in); got != want {
			t.Errorf("PermissionModeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUpdateTaskPermissionModeKeepsBoolInSync guards the single-source-of-truth
// invariant the resume/cycle fix relies on: writing permission_mode must keep the
// legacy dangerous_mode bool consistent, so the badge and the live session can
// never disagree (the root cause of tasks "stored dangerous but still prompting").
func TestUpdateTaskPermissionModeKeepsBoolInSync(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	database := newPermTestDB(t)
	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "p"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	cases := []struct {
		mode          string
		wantEffective string
		wantDangerous bool
	}{
		{PermissionModeDangerous, PermissionModeDangerous, true},
		{PermissionModeAuto, PermissionModeAuto, false}, // dangerous bool must clear
		{PermissionModeAcceptEdits, PermissionModeAcceptEdits, false},
		{PermissionModeDefault, PermissionModeDefault, false},
	}
	for _, c := range cases {
		if err := database.UpdateTaskPermissionMode(task.ID, c.mode); err != nil {
			t.Fatalf("update to %q: %v", c.mode, err)
		}
		got, _ := database.GetTask(task.ID)
		if got.EffectivePermissionMode() != c.wantEffective {
			t.Errorf("after set %q: EffectivePermissionMode = %q, want %q", c.mode, got.EffectivePermissionMode(), c.wantEffective)
		}
		if got.DangerousMode != c.wantDangerous {
			t.Errorf("after set %q: DangerousMode = %v, want %v", c.mode, got.DangerousMode, c.wantDangerous)
		}
	}
}

func TestGlobalDefaultPermissionMode(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	if got := GlobalDefaultPermissionMode(); got != PermissionModeAuto {
		t.Errorf("default global = %q, want %q", got, PermissionModeAuto)
	}
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "default")
	if got := GlobalDefaultPermissionMode(); got != PermissionModeDefault {
		t.Errorf("override global = %q, want %q", got, PermissionModeDefault)
	}
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "garbage")
	if got := GlobalDefaultPermissionMode(); got != PermissionModeAuto {
		t.Errorf("invalid override should fall back to auto, got %q", got)
	}
}

func TestProjectEffectiveDefaultPermissionMode(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	p := &Project{DefaultPermissionMode: PermissionModeDangerous}
	if got := p.EffectiveDefaultPermissionMode(); got != PermissionModeDangerous {
		t.Errorf("got %q, want dangerous", got)
	}
	p = &Project{}
	if got := p.EffectiveDefaultPermissionMode(); got != PermissionModeAuto {
		t.Errorf("unset project should use global default (auto), got %q", got)
	}
}

func newPermTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(dbPath)
	})
	return database
}

func TestCreateTaskInheritsProjectDefault(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	database := newPermTestDB(t)

	if err := database.CreateProject(&Project{Name: "autoproj", Path: t.TempDir(), DefaultPermissionMode: PermissionModeAuto}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "autoproj"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := database.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.EffectivePermissionMode() != PermissionModeAuto {
		t.Errorf("task should inherit auto, got %q", got.EffectivePermissionMode())
	}
	if got.DangerousMode {
		t.Error("auto task should not be dangerous")
	}
}

func TestCreateTaskExplicitModeWins(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	database := newPermTestDB(t)

	if err := database.CreateProject(&Project{Name: "autoproj", Path: t.TempDir(), DefaultPermissionMode: PermissionModeAuto}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "autoproj", PermissionMode: PermissionModeDangerous}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, _ := database.GetTask(task.ID)
	if got.EffectivePermissionMode() != PermissionModeDangerous {
		t.Errorf("explicit dangerous should win, got %q", got.EffectivePermissionMode())
	}
	if !got.DangerousMode {
		t.Error("dangerous task should have DangerousMode synced to true")
	}
}

func TestCreateTaskLegacyDangerousBool(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	database := newPermTestDB(t)

	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "p", DangerousMode: true}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, _ := database.GetTask(task.ID)
	if got.PermissionMode != PermissionModeDangerous {
		t.Errorf("legacy DangerousMode should set permission_mode to dangerous, got %q", got.PermissionMode)
	}
}

func TestUpdateTaskPermissionModeSyncsDangerous(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	database := newPermTestDB(t)
	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "p"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := database.UpdateTaskPermissionMode(task.ID, PermissionModeDangerous); err != nil {
		t.Fatalf("update perm mode: %v", err)
	}
	got, _ := database.GetTask(task.ID)
	if got.PermissionMode != PermissionModeDangerous || !got.DangerousMode {
		t.Errorf("expected dangerous synced, got mode=%q dangerous=%v", got.PermissionMode, got.DangerousMode)
	}

	// Toggling dangerous off via the legacy setter resets to default.
	if err := database.UpdateTaskDangerousMode(task.ID, false); err != nil {
		t.Fatalf("update dangerous: %v", err)
	}
	got, _ = database.GetTask(task.ID)
	if got.PermissionMode != PermissionModeDefault || got.DangerousMode {
		t.Errorf("expected default after safe toggle, got mode=%q dangerous=%v", got.PermissionMode, got.DangerousMode)
	}
}

func TestProjectDefaultPermissionModePersists(t *testing.T) {
	database := newPermTestDB(t)
	if err := database.CreateProject(&Project{Name: "p", Path: t.TempDir(), DefaultPermissionMode: PermissionModeAuto}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	got, err := database.GetProjectByName("p")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if got.DefaultPermissionMode != PermissionModeAuto {
		t.Errorf("project default not persisted, got %q", got.DefaultPermissionMode)
	}

	got.DefaultPermissionMode = PermissionModeDangerous
	if err := database.UpdateProject(got); err != nil {
		t.Fatalf("update project: %v", err)
	}
	again, _ := database.GetProjectByName("p")
	if again.DefaultPermissionMode != PermissionModeDangerous {
		t.Errorf("updated project default not persisted, got %q", again.DefaultPermissionMode)
	}
}

func TestLegacyAutoMigratesToAcceptEditsOnce(t *testing.T) {
	t.Setenv("TASKYOU_DEFAULT_PERMISSION_MODE", "")
	database := newPermTestDB(t)
	if err := database.CreateProject(&Project{Name: "legacy", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &Task{Title: "T", Status: StatusQueued, Type: TypeCode, Project: "legacy"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Simulate pre-migration rows: "auto" stored when it still meant acceptEdits,
	// and clear the guard so the one-time migration runs again.
	database.Exec(`UPDATE tasks SET permission_mode = 'auto' WHERE id = ?`, task.ID)
	database.Exec(`UPDATE projects SET default_permission_mode = 'auto' WHERE name = 'legacy'`)
	database.SetSetting(permModeAutoMigrationKey, "")

	if err := database.migrate(); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}

	got, _ := database.GetTask(task.ID)
	if got.PermissionMode != PermissionModeAcceptEdits {
		t.Errorf("legacy task 'auto' should migrate to accept-edits, got %q", got.PermissionMode)
	}
	proj, _ := database.GetProjectByName("legacy")
	if proj.DefaultPermissionMode != PermissionModeAcceptEdits {
		t.Errorf("legacy project 'auto' should migrate to accept-edits, got %q", proj.DefaultPermissionMode)
	}

	// One-time guard: a task later set to the NEW auto mode must NOT be rewritten
	// on a subsequent boot.
	database.Exec(`UPDATE tasks SET permission_mode = 'auto' WHERE id = ?`, task.ID)
	if err := database.migrate(); err != nil {
		t.Fatalf("re-run migrate (guarded): %v", err)
	}
	got, _ = database.GetTask(task.ID)
	if got.PermissionMode != PermissionModeAuto {
		t.Errorf("guard should preserve new auto-mode task, got %q", got.PermissionMode)
	}
}
