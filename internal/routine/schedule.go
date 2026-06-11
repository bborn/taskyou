package routine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Scheduling is deliberately stateless: ty never records schedules in its own
// database. The OS scheduler is the single source of truth, and ty owns only a
// naming convention inside it — launchd plists labelled
// "com.taskyou.routine.<name>" and crontab lines tagged "# ty:routine:<name>".
// Everything here is CRUD through that convention (the brew-services trick):
// install writes one file/line, remove deletes it, lookups are live reads.
// ty never touches scheduler entries it didn't create, and after install
// returns, ty has no clock-related runtime behavior at all.

const launchdLabelPrefix = "com.taskyou.routine."

// cronMarker tags crontab lines owned by ty so lookups and removals never
// touch the user's own entries.
func cronMarker(name string) string { return "# ty:routine:" + name }

// Schedule describes a live OS-scheduler entry for a routine.
type Schedule struct {
	Backend string // "launchd" or "cron"
	Detail  string // human summary, e.g. "every 30m" or "0 8 * * *"
	Path    string // plist path (launchd) or "crontab" (cron)
}

// launchdDir returns where ty-managed LaunchAgents live.
func launchdDir() string {
	if dir := os.Getenv("TY_ROUTINES_LAUNCHD_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func launchdPlistPath(name string) string {
	return filepath.Join(launchdDir(), launchdLabelPrefix+name+".plist")
}

// launchctlBin and crontabBin are overridable for tests.
var (
	launchctlBin = "launchctl"
	crontabBin   = "crontab"
)

// ScheduleOptions describes the requested cadence. Exactly one of Every or
// Cron must be set: intervals install a launchd agent on macOS (cron
// elsewhere), cron expressions always install a crontab line.
type ScheduleOptions struct {
	Every time.Duration // interval cadence
	Cron  string        // five-field cron expression
}

func (o ScheduleOptions) validate() error {
	if (o.Every == 0) == (o.Cron == "") {
		return fmt.Errorf("specify exactly one of --every or --cron")
	}
	if o.Every != 0 && o.Every < time.Minute {
		return fmt.Errorf("--every must be at least 1m (got %s)", o.Every)
	}
	if o.Cron != "" && len(strings.Fields(o.Cron)) != 5 {
		return fmt.Errorf("--cron expects a five-field expression (got %q)", o.Cron)
	}
	return nil
}

// backendFor picks the scheduler backend for the requested cadence.
func (o ScheduleOptions) backendFor(goos string) string {
	if o.Cron != "" {
		return "cron"
	}
	if goos == "darwin" {
		return "launchd"
	}
	return "cron"
}

// RenderSchedule returns the scheduler config that InstallSchedule would
// install, for --print and for tests.
func RenderSchedule(name string, opts ScheduleOptions) (backend, content string, err error) {
	if err := opts.validate(); err != nil {
		return "", "", err
	}
	backend = opts.backendFor(runtime.GOOS)
	switch backend {
	case "launchd":
		return backend, renderPlist(name, opts.Every), nil
	default:
		line, err := renderCronLine(name, opts)
		return backend, line, err
	}
}

// renderPlist generates the LaunchAgent. Two environment traps are handled
// here so fresh users don't hit them: PATH is captured from the invoking
// shell (launchd's default PATH can't find ty, claude, or bird), and the
// program is wrapped in /bin/bash so macOS's "Background Items Added"
// notification attributes the agent to an Apple-signed binary instead of
// nagging about an unsigned one on every ty upgrade.
func renderPlist(name string, every time.Duration) string {
	tyBin := executablePath()
	logPath := filepath.Join(StateDir(name), "launchd.log")
	cmd := fmt.Sprintf("exec %q run %q", tyBin, name)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s%s</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>%s</string>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>-c</string>
		<string>%s</string>
	</array>
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabelPrefix, name, os.Getenv("HOME"), os.Getenv("PATH"), xmlEscape(cmd), int(every.Seconds()), logPath, logPath)
}

func renderCronLine(name string, opts ScheduleOptions) (string, error) {
	expr := opts.Cron
	if expr == "" {
		var err error
		expr, err = everyToCron(opts.Every)
		if err != nil {
			return "", err
		}
	}
	tyBin := executablePath()
	logPath := filepath.Join(StateDir(name), "cron.log")
	return fmt.Sprintf("%s PATH=%s %q run %q >> %q 2>&1 %s",
		expr, os.Getenv("PATH"), tyBin, name, logPath, cronMarker(name)), nil
}

// everyToCron maps clean intervals onto cron syntax. Intervals cron can't
// express (90m, 7h) error with a pointer to --cron.
func everyToCron(every time.Duration) (string, error) {
	mins := int(every.Minutes())
	switch {
	case mins < 60 && 60%mins == 0:
		return fmt.Sprintf("*/%d * * * *", mins), nil
	case mins%60 == 0 && (mins/60) <= 24 && 24%(mins/60) == 0:
		return fmt.Sprintf("0 */%d * * *", mins/60), nil
	default:
		return "", fmt.Errorf("--every %s doesn't map onto cron; use --cron with an explicit expression", every)
	}
}

// InstallSchedule registers the routine with the OS scheduler and returns the
// live Schedule. Re-installing replaces any previous ty-managed entry.
func InstallSchedule(name string, opts ScheduleOptions) (*Schedule, error) {
	backend, content, err := RenderSchedule(name, opts)
	if err != nil {
		return nil, err
	}

	switch backend {
	case "launchd":
		plist := launchdPlistPath(name)
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
			return nil, fmt.Errorf("create LaunchAgents dir: %w", err)
		}
		// Unload any previous version first; ignore errors (not loaded is fine).
		_ = exec.Command(launchctlBin, "unload", plist).Run()
		if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write plist: %w", err)
		}
		if out, err := exec.Command(launchctlBin, "load", plist).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("launchctl load: %s: %w", strings.TrimSpace(string(out)), err)
		}
	default:
		if err := upsertCronLine(name, content); err != nil {
			return nil, err
		}
	}
	return ScheduledFor(name)
}

// RemoveSchedule removes any ty-managed scheduler entry for the routine.
// Idempotent: removing a routine that isn't scheduled returns (false, nil).
func RemoveSchedule(name string) (bool, error) {
	removed := false

	plist := launchdPlistPath(name)
	if _, err := os.Stat(plist); err == nil {
		_ = exec.Command(launchctlBin, "unload", plist).Run()
		if err := os.Remove(plist); err != nil {
			return false, fmt.Errorf("remove plist: %w", err)
		}
		removed = true
	}

	had, err := removeCronLine(name)
	if err != nil {
		return removed, err
	}
	return removed || had, nil
}

// ScheduledFor returns the routine's live ty-managed schedule, or nil if none
// exists. It reads the OS scheduler directly — there is no cached state to
// disagree with reality.
func ScheduledFor(name string) (*Schedule, error) {
	schedules, err := LoadSchedules([]string{name})
	if err != nil {
		return nil, err
	}
	return schedules[name], nil
}

var startIntervalRe = regexp.MustCompile(`<key>StartInterval</key>\s*<integer>(\d+)</integer>`)

// LoadSchedules resolves live schedules for many routines with one pass over
// the LaunchAgents dir and one crontab read (the TUI and list call this).
func LoadSchedules(names []string) (map[string]*Schedule, error) {
	result := make(map[string]*Schedule)

	for _, name := range names {
		plist := launchdPlistPath(name)
		data, err := os.ReadFile(plist)
		if err != nil {
			continue
		}
		detail := "custom (see plist)"
		if m := startIntervalRe.FindSubmatch(data); m != nil {
			secs, _ := strconv.Atoi(string(m[1]))
			detail = "every " + cleanDuration(time.Duration(secs)*time.Second)
		}
		result[name] = &Schedule{Backend: "launchd", Detail: detail, Path: plist}
	}

	cron, err := readCrontab()
	if err != nil {
		return result, nil // no crontab (or no crontab binary) — launchd results still stand
	}
	for _, name := range names {
		if result[name] != nil {
			continue
		}
		marker := cronMarker(name)
		for _, line := range strings.Split(cron, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), marker) {
				fields := strings.Fields(line)
				detail := "custom"
				if len(fields) >= 5 {
					detail = strings.Join(fields[:5], " ")
				}
				result[name] = &Schedule{Backend: "cron", Detail: detail, Path: "crontab"}
				break
			}
		}
	}
	return result, nil
}

func readCrontab() (string, error) {
	out, err := exec.Command(crontabBin, "-l").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func writeCrontab(content string) error {
	cmd := exec.Command(crontabBin, "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab write: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func upsertCronLine(name, line string) error {
	existing, _ := readCrontab() // empty crontab errors; treat as empty
	var kept []string
	for _, l := range strings.Split(existing, "\n") {
		if l == "" || strings.HasSuffix(strings.TrimSpace(l), cronMarker(name)) {
			continue
		}
		kept = append(kept, l)
	}
	kept = append(kept, line)
	return writeCrontab(strings.Join(kept, "\n") + "\n")
}

func removeCronLine(name string) (bool, error) {
	existing, err := readCrontab()
	if err != nil {
		return false, nil // no crontab at all — nothing to remove
	}
	var kept []string
	found := false
	for _, l := range strings.Split(existing, "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), cronMarker(name)) {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	if !found {
		return false, nil
	}
	content := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if strings.TrimSpace(content) == "" {
		// crontab refuses empty stdin on some systems; remove outright.
		_ = exec.Command(crontabBin, "-r").Run()
		return true, nil
	}
	return true, writeCrontab(content + "\n")
}

// cleanDuration drops zero-valued trailing units from Go's duration format
// ("30m0s" -> "30m", "1h0m0s" -> "1h") for display. The trims are guarded by
// the preceding unit so "30m0s" can't degrade to "3".
func cleanDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "ty"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
