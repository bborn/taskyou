package routine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupScheduleTest isolates the LaunchAgents dir and stubs launchctl and
// crontab. The crontab stub persists state in a temp file so upsert/remove
// round-trips behave like the real thing.
func setupScheduleTest(t *testing.T) (launchdAgents string, cronFile string) {
	t.Helper()
	launchdAgents = t.TempDir()
	t.Setenv("TY_ROUTINES_LAUNCHD_DIR", launchdAgents)
	t.Setenv("TY_ROUTINES_STATE_DIR", filepath.Join(t.TempDir(), "state"))

	binDir := t.TempDir()
	cronFile = filepath.Join(binDir, "crontab.state")

	launchctl := filepath.Join(binDir, "launchctl-stub")
	if err := os.WriteFile(launchctl, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write launchctl stub: %v", err)
	}
	crontab := filepath.Join(binDir, "crontab-stub")
	script := `#!/bin/bash
STATE="` + cronFile + `"
case "$1" in
  -l) [ -f "$STATE" ] || exit 1; cat "$STATE" ;;
  -r) rm -f "$STATE" ;;
  -)  cat > "$STATE" ;;
esac
`
	if err := os.WriteFile(crontab, []byte(script), 0o755); err != nil {
		t.Fatalf("write crontab stub: %v", err)
	}

	prevLaunchctl, prevCrontab := launchctlBin, crontabBin
	launchctlBin, crontabBin = launchctl, crontab
	t.Cleanup(func() { launchctlBin, crontabBin = prevLaunchctl, prevCrontab })
	return launchdAgents, cronFile
}

func TestScheduleOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    ScheduleOptions
		wantErr string
	}{
		{"neither", ScheduleOptions{}, "exactly one"},
		{"both", ScheduleOptions{Every: time.Hour, Cron: "0 8 * * *"}, "exactly one"},
		{"too frequent", ScheduleOptions{Every: 30 * time.Second}, "at least 1m"},
		{"bad cron", ScheduleOptions{Cron: "hourly"}, "five-field"},
		{"good every", ScheduleOptions{Every: 30 * time.Minute}, ""},
		{"good cron", ScheduleOptions{Cron: "0 8 * * 1-5"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestEveryToCron(t *testing.T) {
	tests := []struct {
		every   time.Duration
		want    string
		wantErr bool
	}{
		{30 * time.Minute, "*/30 * * * *", false},
		{15 * time.Minute, "*/15 * * * *", false},
		{time.Hour, "0 */1 * * *", false},
		{6 * time.Hour, "0 */6 * * *", false},
		{90 * time.Minute, "", true},
		{7 * time.Hour, "", true},
	}
	for _, tt := range tests {
		got, err := everyToCron(tt.every)
		if tt.wantErr {
			if err == nil {
				t.Errorf("everyToCron(%s): expected error, got %q", tt.every, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("everyToCron(%s) = %q, %v; want %q", tt.every, got, err, tt.want)
		}
	}
}

func TestCleanDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{45 * time.Second, "45s"},
		{61 * time.Minute, "1h1m"},
	}
	for _, tt := range tests {
		if got := cleanDuration(tt.in); got != tt.want {
			t.Errorf("cleanDuration(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderPlistContent(t *testing.T) {
	setupScheduleTest(t)
	content := renderPlist("scout", 30*time.Minute)

	for _, want := range []string{
		"<string>com.taskyou.routine.scout</string>",
		"<integer>1800</integer>",
		"<string>/bin/bash</string>", // signed launcher, not the bare ty binary
		"<key>PATH</key>",
		`run &quot;scout&quot;`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("plist missing %q:\n%s", want, content)
		}
	}
	if !strings.Contains(content, os.Getenv("PATH")) {
		t.Error("plist should capture the invoking shell's PATH")
	}
}

func TestInstallAndRemoveLaunchdSchedule(t *testing.T) {
	agents, _ := setupScheduleTest(t)

	sched, err := InstallSchedule("scout", ScheduleOptions{Every: 30 * time.Minute})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if sched == nil || sched.Backend != "launchd" || sched.Detail != "every 30m" {
		t.Fatalf("unexpected schedule: %+v", sched)
	}
	plist := filepath.Join(agents, "com.taskyou.routine.scout.plist")
	if _, err := os.Stat(plist); err != nil {
		t.Fatalf("plist not written: %v", err)
	}

	// Live lookup sees it; an unrelated routine doesn't.
	schedules, err := LoadSchedules([]string{"scout", "other"})
	if err != nil {
		t.Fatalf("load schedules: %v", err)
	}
	if schedules["scout"] == nil || schedules["other"] != nil {
		t.Fatalf("lookup mismatch: %+v", schedules)
	}

	removed, err := RemoveSchedule("scout")
	if err != nil || !removed {
		t.Fatalf("remove: %v removed=%v", err, removed)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Error("plist should be deleted")
	}
	// Idempotent second removal.
	removed, err = RemoveSchedule("scout")
	if err != nil || removed {
		t.Fatalf("second remove should be a no-op: %v removed=%v", err, removed)
	}
}

func TestInstallAndRemoveCronSchedule(t *testing.T) {
	_, cronFile := setupScheduleTest(t)

	// Pre-existing user line must survive untouched.
	if err := os.WriteFile(cronFile, []byte("0 7 * * * /usr/local/bin/backup.sh\n"), 0o644); err != nil {
		t.Fatalf("seed crontab: %v", err)
	}

	sched, err := InstallSchedule("scout", ScheduleOptions{Cron: "0 8 * * 1-5"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if sched == nil || sched.Backend != "cron" || sched.Detail != "0 8 * * 1-5" {
		t.Fatalf("unexpected schedule: %+v", sched)
	}

	data, _ := os.ReadFile(cronFile)
	if !strings.Contains(string(data), "backup.sh") {
		t.Error("user's own crontab line was clobbered")
	}
	if !strings.Contains(string(data), "# ty:routine:scout") {
		t.Errorf("ty line missing from crontab:\n%s", data)
	}

	// Re-install replaces, not duplicates.
	if _, err := InstallSchedule("scout", ScheduleOptions{Cron: "30 8 * * *"}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	data, _ = os.ReadFile(cronFile)
	if strings.Count(string(data), "# ty:routine:scout") != 1 {
		t.Errorf("expected exactly one ty line after reinstall:\n%s", data)
	}

	removed, err := RemoveSchedule("scout")
	if err != nil || !removed {
		t.Fatalf("remove: %v removed=%v", err, removed)
	}
	data, _ = os.ReadFile(cronFile)
	if strings.Contains(string(data), "ty:routine:scout") {
		t.Error("ty line should be removed")
	}
	if !strings.Contains(string(data), "backup.sh") {
		t.Error("user's own line should survive removal")
	}
}

func TestRenderScheduleCronForExplicitExpression(t *testing.T) {
	setupScheduleTest(t)
	backend, content, err := RenderSchedule("scout", ScheduleOptions{Cron: "*/10 * * * *"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if backend != "cron" {
		t.Errorf("explicit cron expression should always use the cron backend, got %q", backend)
	}
	if !strings.Contains(content, "*/10 * * * *") || !strings.Contains(content, "# ty:routine:scout") {
		t.Errorf("unexpected cron line: %q", content)
	}
}
