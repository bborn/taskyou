package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePermissionMode(t *testing.T) {
	cases := map[string]string{
		"auto":         PermissionModeAuto, // legacy alias, kept for back-compat
		"accept-edits": PermissionModeAuto, // unambiguous spelling normalizes to the canonical value
		"accept_edits": PermissionModeAuto,
		"acceptEdits":  PermissionModeAuto, // Claude Code's own spelling
		"  Auto  ":     PermissionModeAuto, // trimmed + case-insensitive
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
