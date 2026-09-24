package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// writeTree writes content at root/rel, creating directories.
func writeTree(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// syncSource builds a ~/.claude like this machine's: plain skills, one
// symlinked in from a project, one with a macOS binary, node_modules, a .git
// and a setup script, the account-synced skills, and four plugins — one on,
// one off, one from a marketplace that exists only here, one that will fail.
func syncSource(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "claude")

	writeTree(t, src, "skills/plain/SKILL.md", "---\nname: plain\n---\n", 0o644)

	writeTree(t, src, "skills/builder/SKILL.md", "builder", 0o644)
	writeTree(t, src, "skills/builder/setup", "#!/bin/sh\ndate > \"$HOME/setup-ran\"\n", 0o755)
	writeTree(t, src, "skills/builder/bin/tool", "\xcf\xfa\xed\xfe macho", 0o755)
	writeTree(t, src, "skills/builder/bin/helper.sh", "#!/bin/sh\necho hi\n", 0o755)
	writeTree(t, src, "skills/builder/node_modules/dep/index.js", "module.exports = 1", 0o644)
	writeTree(t, src, "skills/builder/.git/HEAD", "ref: refs/heads/main", 0o644)
	if err := os.Symlink(filepath.Join(base, "nowhere"), filepath.Join(src, "skills/builder/dangling")); err != nil {
		t.Fatal(err)
	}

	// A skill that lives in a project and is symlinked in, with a symlinked
	// file inside it: both must arrive as real files.
	writeTree(t, base, "project/skill/SKILL.md", "linked", 0o644)
	writeTree(t, base, "project/shared.md", "shared", 0o644)
	if err := os.Symlink(filepath.Join(base, "project/shared.md"), filepath.Join(base, "project/skill/shared.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "project/skill"), filepath.Join(src, "skills/linked")); err != nil {
		t.Fatal(err)
	}

	writeTree(t, src, "skills/synced/org_user/account-skill/SKILL.md", "the account's", 0o644)

	writeTree(t, src, "settings.json", `{"enabledPlugins":{"good@mk":true,"off@mk":false,"local@dirmk":true,"broken@mk":true},"hooks":{"Stop":[]}}`, 0o644)
	writeTree(t, src, "plugins/known_marketplaces.json", `{"mk":{"source":{"source":"github","repo":"owner/repo"}},"dirmk":{"source":{"source":"directory","path":"/Users/me/plugin"}}}`, 0o644)
	writeTree(t, src, "plugins/installed_plugins.json", `{"version":2,"plugins":{"good@mk":[{"scope":"user","version":"1.0.0","gitCommitSha":"abc"}]}}`, 0o644)
	return src
}

func itemNamed(m hostManifest, kind, name string) (syncItem, bool) {
	for _, it := range m.Items {
		if it.Kind == kind && it.Name == name {
			return it, true
		}
	}
	return syncItem{}, false
}

func TestHostManifestHashesWhatWouldBeSent(t *testing.T) {
	src := syncSource(t)
	m, err := buildHostManifest(src)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, it := range m.Items {
		names = append(names, it.Kind+":"+it.Name)
	}
	if got := strings.Join(names, " "); got != "skill:builder skill:linked skill:plain plugin:broken@mk plugin:good@mk" {
		t.Fatalf("items = %s", got)
	}
	if !strings.Contains(strings.Join(m.Skipped, "\n"), "local@dirmk") {
		t.Errorf("a plugin from a marketplace that only exists here was not reported: %v", m.Skipped)
	}

	builder, _ := itemNamed(m, "skill", "builder")
	if !builder.Setup {
		t.Error("builder's ./setup was not noticed")
	}
	ex := strings.Join(builder.Excludes, " ")
	if !strings.Contains(ex, "/builder/bin/tool") || !strings.Contains(ex, "/builder/dangling") || strings.Contains(ex, "helper.sh") {
		t.Errorf("excludes = %v, want the Mach-O binary and the dangling link, not the shell script", builder.Excludes)
	}
	good, _ := itemNamed(m, "plugin", "good@mk")
	if good.Source != "owner/repo" || good.Marketplace != "mk" {
		t.Errorf("good@mk = %+v", good)
	}

	// Stable when nothing changed.
	again, _ := buildHostManifest(src)
	if again.Hash != m.Hash {
		t.Fatal("the manifest hash changed with nothing changed")
	}

	// What is not sent does not count: a rebuilt node_modules is not a change.
	writeTree(t, src, "skills/builder/node_modules/dep/index.js", "module.exports = 2 // rebuilt", 0o644)
	if again, _ = buildHostManifest(src); again.Hash != m.Hash {
		t.Error("a change inside node_modules changed the manifest")
	}

	// What is sent does, for that skill alone.
	future := time.Now().Add(time.Hour)
	writeTree(t, src, "skills/plain/SKILL.md", "---\nname: plain\ndescription: edited\n---\n", 0o644)
	_ = os.Chtimes(filepath.Join(src, "skills/plain/SKILL.md"), future, future)
	after, _ := buildHostManifest(src)
	if after.Hash == m.Hash {
		t.Fatal("editing a skill did not change the manifest")
	}
	before, _ := itemNamed(m, "skill", "plain")
	now, _ := itemNamed(after, "skill", "plain")
	unchanged, _ := itemNamed(after, "skill", "builder")
	if before.Hash == now.Hash || unchanged.Hash != builder.Hash {
		t.Error("the edit was not attributed to the edited skill alone")
	}
}

func TestPlanHostSync(t *testing.T) {
	m := hostManifest{Hash: "m1", Items: []syncItem{
		{Kind: "skill", Name: "a", Hash: "ha"},
		{Kind: "skill", Name: "b", Hash: "hb2"},
		{Kind: "plugin", Name: "p@mk", Hash: "hp"},
		{Kind: "plugin", Name: "late@mk", Hash: "hl"},
		{Kind: "plugin", Name: "stuck@mk", Hash: "hs"},
	}}
	now := time.Now()
	st := hostSyncState{Target: "/home/u/.claude", Items: map[string]hostSyncRecord{
		"skill a":          {Hash: "ha"},
		"skill b":          {Hash: "hb1"}, // changed here
		"skill gone":       {Hash: "hg"},  // ty put it there; no longer here
		"plugin p@mk":      {Hash: "hp"},
		"plugin late@mk":   {Hash: "hl", FailedAt: now.Add(-25 * time.Hour)}, // due a retry
		"plugin stuck@mk":  {Hash: "hs", FailedAt: now.Add(-time.Hour)},      // not yet
		"plugin old@mk":    {Hash: "ho"},
		"plugin failed@mk": {Hash: "hf", FailedAt: now.Add(-time.Hour)},
	}}

	p := planHostSync(m, st, "/home/u/.claude", now, false)
	names := func(items []syncItem) string {
		var out []string
		for _, it := range items {
			out = append(out, it.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(p.Skills); got != "b" {
		t.Errorf("skills to copy = %s, want b", got)
	}
	if got := strings.Join(p.RemovedSkills, ","); got != "gone" {
		t.Errorf("skills to remove = %s, want gone", got)
	}
	if got := names(p.Plugins); got != "late@mk" {
		t.Errorf("plugins to install = %s, want late@mk (stuck@mk waits out its day)", got)
	}
	// A plugin that never installed is not "disabled" when it goes away.
	if got := strings.Join(p.RemovedPlugins, ","); got != "old@mk" {
		t.Errorf("plugins to disable = %s, want old@mk", got)
	}
	if len(p.Keep) != 3 {
		t.Errorf("kept = %v, want a, p@mk and stuck@mk", p.Keep)
	}

	// A record about another directory is no record at all — and removing what
	// it lists would remove from the wrong place.
	moved := planHostSync(m, st, "/elsewhere/.claude", now, false)
	if len(moved.Skills) != 2 || len(moved.Plugins) != 3 || len(moved.RemovedSkills)+len(moved.RemovedPlugins) != 0 {
		t.Errorf("after the config dir moved: %+v", moved)
	}
	if forced := planHostSync(m, st, "/home/u/.claude", now, true); len(forced.Skills)+len(forced.Plugins) != 5 {
		t.Errorf("force redid %d items, want all 5", len(forced.Skills)+len(forced.Plugins))
	}
}

// dirSyncTransport is a "host" that is a directory on this machine: scripts run
// in sh with HOME pointed at it, and skills are rsynced into it with the same
// rules the ssh transport uses.
type dirSyncTransport struct {
	home, bin string

	mu      sync.Mutex
	runs    int
	rsyncs  int
	failRun error
	block   chan struct{}
}

func (d *dirSyncTransport) Run(ctx context.Context, script string) (string, error) {
	d.mu.Lock()
	d.runs++
	fail, block := d.failRun, d.block
	d.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return "", errors.New("host did not answer")
	}
	if fail != nil {
		return "", fail
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+d.home, "CLAUDE_CONFIG_DIR=", "PATH="+d.bin+":"+os.Getenv("PATH"))
	out, err := cmd.Output()
	return string(out), err
}

func (d *dirSyncTransport) Rsync(ctx context.Context, srcRoot string, names, excludes []string, dest string) (bool, error) {
	d.mu.Lock()
	d.rsyncs++
	d.mu.Unlock()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return false, err
	}
	return runRsync(ctx, srcRoot, names, excludes, dest+"/")
}

func (d *dirSyncTransport) counts() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runs, d.rsyncs
}

// fakeHost is a directory standing in for a fleet host, with a claude that
// logs what it was asked and cannot install broken@mk.
func fakeHost(t *testing.T) *dirSyncTransport {
	t.Helper()
	home := t.TempDir()
	bin := t.TempDir()
	writeTree(t, bin, "claude", `#!/bin/sh
echo "$*" >>"$HOME/claude.log"
case "$*" in
  *broken@mk*) echo "plugin needs a confirmed command"; exit 1 ;;
esac
exit 0
`, 0o755)
	// What the host already had, which is not ty's to touch.
	writeTree(t, home, ".claude/CLAUDE.md", "the host's own instructions", 0o644)
	writeTree(t, home, ".claude/skills/host-only/SKILL.md", "installed there by hand", 0o644)
	writeTree(t, home, ".claude/skills/synced/org_user/account-skill/SKILL.md", "the account's", 0o644)
	return &dirSyncTransport{home: home, bin: bin}
}

func testSyncer(src string, tr syncTransport) *hostSyncer {
	s := newHostSyncer()
	s.sourceDir = func() string { return src }
	s.transport = func(string) syncTransport { return tr }
	return s
}

func readHostFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Errorf("host is missing %s: %v", rel, err)
	}
	return string(b)
}

func TestHostSyncCopiesInstallsAndThenSkips(t *testing.T) {
	src := syncSource(t)
	host := fakeHost(t)
	s := testSyncer(src, host)
	ctx := context.Background()

	res, err := s.Sync(ctx, "far-host", false)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if res.UpToDate || len(res.Skills) != 3 {
		t.Fatalf("first sync = %+v, want three skills copied", res)
	}
	skills := filepath.Join(host.home, ".claude", "skills")

	// Copied, with symlinks resolved into real files.
	_ = readHostFile(t, skills, "plain/SKILL.md")
	if got := readHostFile(t, skills, "linked/shared.md"); got != "shared" {
		t.Errorf("linked/shared.md = %q", got)
	}
	if info, err := os.Lstat(filepath.Join(skills, "linked")); err == nil && info.Mode()&os.ModeSymlink != 0 {
		t.Error("linked arrived as a symlink; it points at a path the host does not have")
	}
	_ = readHostFile(t, skills, "builder/bin/helper.sh")
	// Not copied: the macOS binary, node_modules, .git, the account's skills.
	for _, absent := range []string{"builder/bin/tool", "builder/node_modules", "builder/.git", "builder/dangling"} {
		if _, err := os.Stat(filepath.Join(skills, absent)); err == nil {
			t.Errorf("%s was copied to the host", absent)
		}
	}
	// Left alone: what the host had that ty did not put there.
	if got := readHostFile(t, host.home, ".claude/CLAUDE.md"); got != "the host's own instructions" {
		t.Errorf("the host's CLAUDE.md was touched: %q", got)
	}
	_ = readHostFile(t, skills, "host-only/SKILL.md")
	_ = readHostFile(t, skills, "synced/org_user/account-skill/SKILL.md")

	// The changed skill's setup ran there (detached, so give it a moment).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(host.home, "setup-ran")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("builder's setup never ran on the host")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Plugins installed from their marketplace, by the host's own claude.
	log := readHostFile(t, host.home, "claude.log")
	for _, want := range []string{"plugin marketplace add owner/repo", "plugin install good@mk", "plugin enable good@mk", "plugin install broken@mk"} {
		if !strings.Contains(log, want) {
			t.Errorf("claude on the host was never asked to %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "off@mk") || strings.Contains(log, "local@dirmk") {
		t.Errorf("a disabled or here-only plugin was installed:\n%s", log)
	}
	if len(res.Plugins) != 1 || len(res.PluginFailures) != 1 || !strings.Contains(res.PluginFailures[0], "needs a confirmed command") {
		t.Errorf("plugins = %v, failures = %v", res.Plugins, res.PluginFailures)
	}

	// The failure is recorded with its time, so it is not retried every spawn;
	// and with a failure outstanding there is no whole-manifest record.
	state := readHostFile(t, host.home, ".ty-sync/state")
	if !strings.Contains(state, "plugin good@mk ") || !strings.Contains(state, "plugin broken@mk ") || !strings.Contains(state, " failed ") {
		t.Errorf("state =\n%s", state)
	}
	if strings.Contains(state, "manifest ") {
		t.Errorf("state claims the whole manifest landed with a plugin failing:\n%s", state)
	}

	// Nothing changed since: one check, nothing sent, nothing retried.
	runs, rsyncs := host.counts()
	res, err = s.Sync(ctx, "far-host", false)
	if err != nil || !res.UpToDate {
		t.Fatalf("second sync = %+v, %v; want up to date", res, err)
	}
	if r, rs := host.counts(); r != runs+1 || rs != rsyncs {
		t.Errorf("an up-to-date host cost %d commands and %d copies, want 1 and 0", r-runs, rs-rsyncs)
	}

	// A skill removed here goes from the host — only because ty put it there.
	if err := os.RemoveAll(filepath.Join(src, "skills/plain")); err != nil {
		t.Fatal(err)
	}
	// A binary the host built for itself survives a re-sync of its skill.
	writeTree(t, skills, "builder/bin/tool", "linux build", 0o755)
	writeTree(t, src, "skills/builder/SKILL.md", "builder, edited", 0o644)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(src, "skills/builder/SKILL.md"), future, future)
	s.cachedAt = time.Time{}
	res, err = s.Sync(ctx, "far-host", false)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if strings.Join(res.Skills, ",") != "builder" || strings.Join(res.RemovedSkills, ",") != "plain" {
		t.Errorf("third sync = %+v, want builder copied and plain removed", res)
	}
	if _, err := os.Stat(filepath.Join(skills, "plain")); err == nil {
		t.Error("a skill removed here is still on the host")
	}
	_ = readHostFile(t, skills, "host-only/SKILL.md")
	if got := readHostFile(t, skills, "builder/bin/tool"); got != "linux build" {
		t.Errorf("the host's own build of an excluded binary was replaced: %q", got)
	}
}

// With everything in place the record says so, and the next check is a single
// comparison.
func TestHostSyncRecordsACompleteManifest(t *testing.T) {
	src := syncSource(t)
	writeTree(t, src, "settings.json", `{"enabledPlugins":{"good@mk":true}}`, 0o644)
	host := fakeHost(t)
	s := testSyncer(src, host)

	if _, err := s.Sync(context.Background(), "far-host", false); err != nil {
		t.Fatal(err)
	}
	m, _ := s.manifest()
	if state := readHostFile(t, host.home, ".ty-sync/state"); !strings.Contains(state, "manifest "+m.Hash) {
		t.Errorf("state has no manifest record:\n%s", state)
	}
	res, err := s.Sync(context.Background(), "far-host", false)
	if err != nil || !res.UpToDate {
		t.Errorf("second sync = %+v, %v", res, err)
	}
}

func syncTestTask(t *testing.T) (*Executor, *db.DB, *db.Task) {
	t.Helper()
	e, database := reconcileTestExecutor(t)
	task := &db.Task{Title: "placed", Type: "task", Project: "test"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return e, database, task
}

func taskLogText(t *testing.T, database *db.DB, id int64) string {
	t.Helper()
	logs, err := database.GetTaskLogs(id, 100)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range logs {
		b.WriteString(l.Content + "\n")
	}
	return b.String()
}

// A sync that fails, or never answers, must not stand between a task and its
// spawn: the failure is logged, and a slow sync is left to finish on its own.
func TestHostSyncNeverBlocksTheSpawn(t *testing.T) {
	src := syncSource(t)

	t.Run("fails", func(t *testing.T) {
		e, database, task := syncTestTask(t)
		host := fakeHost(t)
		host.failRun = errors.New("connection refused")
		e.hostSyncOnce.Do(func() { e.hostSync = testSyncer(src, host) })

		start := time.Now()
		e.syncHostTools(context.Background(), task, "far-host")
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("a failed sync held the spawn for %s", took)
		}
		if log := taskLogText(t, database, task.ID); !strings.Contains(log, "Could not bring skills and plugins on far-host up to date") ||
			!strings.Contains(log, "launching with what it has") {
			t.Errorf("task log =\n%s", log)
		}
	})

	t.Run("hangs", func(t *testing.T) {
		e, database, task := syncTestTask(t)
		host := fakeHost(t)
		host.block = make(chan struct{})
		t.Cleanup(func() { close(host.block) })
		e.hostSyncOnce.Do(func() { e.hostSync = testSyncer(src, host) })
		prev := hostSyncWaitFor
		hostSyncWaitFor = func() time.Duration { return 200 * time.Millisecond }
		t.Cleanup(func() { hostSyncWaitFor = prev })

		start := time.Now()
		e.syncHostTools(context.Background(), task, "far-host")
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("a hanging sync held the spawn for %s", took)
		}
		if log := taskLogText(t, database, task.ID); !strings.Contains(log, "starting without waiting") {
			t.Errorf("task log =\n%s", log)
		}
	})
}
