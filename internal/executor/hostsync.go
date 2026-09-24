package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// Placed hosts get this machine's skills and plugins.
//
// A task placed on a host should have the tools it would have here. What the
// Claude ACCOUNT carries (claude.ai connectors, account-synced skills under
// skills/synced/) already follows the login to every host; what lives only as
// files in ~/.claude does not, and that is most of it. So before each placed
// spawn ty compares a manifest of this machine's user skills and enabled
// plugins with the copy it last left on the host, and brings the host up to date
// when they differ:
//
//   - skills are rsynced with -L (many are symlinks into ~/Projects), without
//     node_modules, .git or the macOS binaries some ship (gstack), and a skill's
//     own ./setup is run on the host when the skill changed, to build what was
//     left behind;
//   - plugins are INSTALLED there from their marketplaces, never copied: a plugin
//     cache is platform-specific and its MCP credentials live in this machine's
//     keychain, and copying Claude credentials between machines makes their
//     refresh tokens fight.
//
// It deliberately does not touch CLAUDE.md, ~/.claude-shared, settings.json
// permissions or hooks: each host keeps its own instructions.
//
// When nothing changed — the normal case — it costs one short command over the
// multiplexed ssh connection. A failure is logged and the task launches anyway.

const (
	// hostSyncWait is how long a spawn waits for a sync before starting without
	// it. The first sync to a new host copies a lot; the spawn should not.
	hostSyncWait = 2 * time.Minute
	// hostSyncTimeout bounds a whole sync, which carries on in the background
	// past hostSyncWait.
	hostSyncTimeout = 15 * time.Minute
	// hostSyncCheckTimeout bounds the one-command check.
	hostSyncCheckTimeout = 30 * time.Second
	// hostSyncRetryFailed is how long a plugin that failed to install waits before
	// it is tried again. Without it a plugin that cannot install there (a
	// marketplace that wants a confirmed command) costs every spawn a retry.
	hostSyncRetryFailed = 24 * time.Hour
	// hostSyncManifestTTL is how long a built manifest is reused. Several tasks
	// spawning together should not each walk ~/.claude/skills.
	hostSyncManifestTTL = 30 * time.Second
	// maxSyncedSkillBytes skips a skill too big to be a skill: a symlink into a
	// project's build tree, say.
	maxSyncedSkillBytes = 512 << 20
	// hostSyncStateDir is where a host keeps ty's record of what it put there.
	hostSyncStateDir = "$HOME/.ty-sync"
)

// hostSyncVersion salts the manifest hash, so a change in what is synced (or
// how) makes every host look out of date once.
const hostSyncVersion = "ty-host-sync 1"

// syncSkipDirs are never copied. node_modules and virtualenvs are
// platform-specific and rebuilt by a skill's setup; .git is history, not skill.
var syncSkipDirs = map[string]bool{"node_modules": true, ".git": true, ".venv": true, "venv": true, "__pycache__": true}

var (
	skillNameRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	pluginIDRe   = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)
	githubRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// syncItem is one skill or plugin in a manifest.
type syncItem struct {
	Kind string // "skill" or "plugin"
	Name string // the skill's directory name, or plugin@marketplace
	Hash string

	// Skills.
	Setup    bool     // has an executable ./setup to run on the host
	Excludes []string // rsync patterns, rooted at the skills directory

	// Plugins.
	Marketplace string
	Source      string // what `claude plugin marketplace add` takes
}

func (i syncItem) key() string { return i.Kind + " " + i.Name }

// hostManifest is everything this machine would put on a host.
type hostManifest struct {
	Hash  string
	Items []syncItem
	// Skipped says what was left out and why.
	Skipped []string
}

// buildHostManifest reads configDir (this machine's ~/.claude).
func buildHostManifest(configDir string) (hostManifest, error) {
	var m hostManifest
	skills, skipped, err := skillItems(filepath.Join(configDir, "skills"))
	if err != nil {
		return m, err
	}
	m.Skipped = append(m.Skipped, skipped...)
	plugins, skipped := pluginItems(configDir)
	m.Skipped = append(m.Skipped, skipped...)
	m.Items = append(skills, plugins...)

	h := sha256.New()
	fmt.Fprintln(h, hostSyncVersion)
	for _, it := range m.Items {
		fmt.Fprintf(h, "%s %s\n", it.key(), it.Hash)
	}
	m.Hash = hex.EncodeToString(h.Sum(nil))[:24]
	return m, nil
}

// skillItems lists the user skills under dir. The account-synced ones
// (skills/synced) are left alone: the account already puts them everywhere.
func skillItems(dir string) ([]syncItem, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var items []syncItem
	var skipped []string
	for _, e := range entries {
		name := e.Name()
		if name == "synced" || strings.HasPrefix(name, ".") {
			continue
		}
		if !skillNameRe.MatchString(name) {
			skipped = append(skipped, fmt.Sprintf("skill %q: a name ty will not copy", name))
			continue
		}
		info, err := os.Stat(filepath.Join(dir, name)) // follows a symlinked skill
		if err != nil || !info.IsDir() {
			continue
		}
		item, err := hashSkill(dir, name)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("skill %s: %v", name, err))
			continue
		}
		items = append(items, item)
	}
	return items, skipped, nil
}

// hashSkill fingerprints one skill by what rsync would send: every file's path,
// size, mode and modification time, following symlinks as -L does. The files
// rsync is told to skip are listed as excludes instead.
func hashSkill(root, name string) (syncItem, error) {
	item := syncItem{Kind: "skill", Name: name}
	h := sha256.New()
	var total int64
	visited := map[string]bool{}

	var walk func(dir, rel string, depth int) error
	walk = func(dir, rel string, depth int) error {
		if depth > 40 {
			return fmt.Errorf("directories nest too deep under %s", rel)
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return err
		}
		if visited[real] {
			// A symlink back up the tree: rsync -L would refuse it too.
			item.Excludes = append(item.Excludes, rsyncLiteral("/"+rel))
			return nil
		}
		visited[real] = true
		defer delete(visited, real)

		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			n := e.Name()
			if n == ".DS_Store" {
				continue
			}
			full := filepath.Join(dir, n)
			childRel := rel + "/" + n
			if strings.ContainsAny(n, "\n\r") {
				return fmt.Errorf("a file name with a line break in it")
			}
			info, err := os.Stat(full)
			if err != nil {
				// A dangling symlink. Left in, rsync -L reports an I/O error and
				// then refuses to delete anything for the whole run.
				item.Excludes = append(item.Excludes, rsyncLiteral("/"+childRel))
				continue
			}
			switch {
			case info.IsDir() && syncSkipDirs[n]:
				// Matches the rsync rule ("node_modules/"), which is for directories.
			case info.IsDir():
				if err := walk(full, childRel, depth+1); err != nil {
					return err
				}
			case !info.Mode().IsRegular():
				item.Excludes = append(item.Excludes, rsyncLiteral("/"+childRel))
			case info.Mode()&0o111 != 0 && isMachO(full):
				// A binary built for this Mac. It cannot run on a Linux host, and
				// excluding it also protects the one the skill's setup builds
				// there from being deleted by the next sync.
				item.Excludes = append(item.Excludes, rsyncLiteral("/"+childRel))
			default:
				total += info.Size()
				if total > maxSyncedSkillBytes {
					return fmt.Errorf("larger than %d MB without node_modules, .git and binaries", maxSyncedSkillBytes>>20)
				}
				fmt.Fprintf(h, "%s\t%d\t%d\t%o\n", childRel, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm())
				if childRel == name+"/setup" && info.Mode()&0o111 != 0 {
					item.Setup = true
				}
			}
		}
		return nil
	}
	if err := walk(filepath.Join(root, name), name, 0); err != nil {
		return item, err
	}
	sort.Strings(item.Excludes)
	for _, x := range item.Excludes {
		fmt.Fprintf(h, "exclude\t%s\n", x)
	}
	item.Hash = hex.EncodeToString(h.Sum(nil))[:24]
	return item, nil
}

// isMachO reports whether a file is a macOS executable or library.
func isMachO(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	switch string(magic[:]) {
	case "\xfe\xed\xfa\xce", "\xfe\xed\xfa\xcf", "\xce\xfa\xed\xfe", "\xcf\xfa\xed\xfe", "\xca\xfe\xba\xbe", "\xca\xfe\xba\xbf":
		return true
	}
	return false
}

// rsyncLiteral escapes a path for an rsync filter rule, which would otherwise
// read *, ? and [ as wildcards.
func rsyncLiteral(p string) string {
	var b strings.Builder
	for _, c := range p {
		if strings.ContainsRune(`*?[\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// pluginItems lists the plugins enabled here, with what a host needs to install
// each: its marketplace's source and the version installed here.
func pluginItems(configDir string) ([]syncItem, []string) {
	var settings struct {
		EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
	}
	if data, err := os.ReadFile(filepath.Join(configDir, "settings.json")); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope        string `json:"scope"`
			Version      string `json:"version"`
			GitCommitSha string `json:"gitCommitSha"`
		} `json:"plugins"`
	}
	if data, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json")); err == nil {
		_ = json.Unmarshal(data, &installed)
	}
	var marketplaces map[string]struct {
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
		} `json:"source"`
	}
	if data, err := os.ReadFile(filepath.Join(configDir, "plugins", "known_marketplaces.json")); err == nil {
		_ = json.Unmarshal(data, &marketplaces)
	}

	var ids []string
	for id, v := range settings.EnabledPlugins {
		if string(bytes.TrimSpace(v)) == "true" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	var items []syncItem
	var skipped []string
	for _, id := range ids {
		if !pluginIDRe.MatchString(id) {
			skipped = append(skipped, fmt.Sprintf("plugin %q: not a plugin@marketplace name", id))
			continue
		}
		_, market, _ := strings.Cut(id, "@")
		mk, ok := marketplaces[market]
		if !ok {
			skipped = append(skipped, fmt.Sprintf("plugin %s: marketplace %s is not known here", id, market))
			continue
		}
		var source string
		switch mk.Source.Source {
		case "github":
			if githubRepoRe.MatchString(mk.Source.Repo) {
				source = mk.Source.Repo
			}
		case "git", "url":
			if u := mk.Source.URL; u != "" && !strings.ContainsAny(u, " \t\r\n") && !strings.HasPrefix(u, "-") {
				source = u
			}
		}
		if source == "" {
			skipped = append(skipped, fmt.Sprintf("plugin %s: its marketplace (%s) exists only on this machine", id, mk.Source.Source))
			continue
		}
		version, sha := "", ""
		for i, inst := range installed.Plugins[id] {
			if i == 0 || inst.Scope == "user" {
				version, sha = inst.Version, inst.GitCommitSha
			}
		}
		sum := sha256.Sum256([]byte(strings.Join([]string{id, source, version, sha}, "\x00")))
		items = append(items, syncItem{
			Kind: "plugin", Name: id, Hash: hex.EncodeToString(sum[:])[:24],
			Marketplace: market, Source: source,
		})
	}
	return items, skipped
}

// hostSyncState is ty's record, kept on the host, of what it put there.
type hostSyncState struct {
	// Target is the host's Claude config dir the record is about.
	Target   string
	Manifest string
	Items    map[string]hostSyncRecord
}

type hostSyncRecord struct {
	Hash     string
	FailedAt time.Time // zero unless this plugin failed to install
}

// parseHostSyncState reads the check command's output. Anything else a login
// shell prints on stdout is ignored.
func parseHostSyncState(out string) (dir string, st hostSyncState) {
	st.Items = map[string]hostSyncRecord{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "dir":
			dir = f[1]
		case len(f) == 2 && f[0] == "target":
			st.Target = f[1]
		case len(f) == 2 && f[0] == "manifest":
			st.Manifest = f[1]
		case len(f) >= 3 && (f[0] == "skill" || f[0] == "plugin"):
			rec := hostSyncRecord{Hash: f[2]}
			if len(f) == 5 && f[3] == "failed" {
				if at, err := strconv.ParseInt(f[4], 10, 64); err == nil {
					rec.FailedAt = time.Unix(at, 0)
				}
			}
			st.Items[f[0]+" "+f[1]] = rec
		}
	}
	return dir, st
}

// hostSyncPlan is what a sync has to do.
type hostSyncPlan struct {
	Skills         []syncItem // to copy
	RemovedSkills  []string   // ty put these there; this machine no longer has them
	Plugins        []syncItem // to install or update
	RemovedPlugins []string   // ty enabled these there; this machine no longer does
	// Keep are the records that carry over unchanged.
	Keep []string
}

func (p hostSyncPlan) empty() bool {
	return len(p.Skills)+len(p.RemovedSkills)+len(p.Plugins)+len(p.RemovedPlugins) == 0
}

// planHostSync compares a manifest with a host's record.
func planHostSync(m hostManifest, st hostSyncState, dir string, now time.Time, force bool) hostSyncPlan {
	var p hostSyncPlan
	if st.Target != dir {
		// A record about another directory says nothing about this one, and
		// removing what it lists would remove from the wrong place.
		st = hostSyncState{Items: map[string]hostSyncRecord{}}
	}
	want := map[string]bool{}
	for _, it := range m.Items {
		want[it.key()] = true
		rec, ok := st.Items[it.key()]
		changed := force || !ok || rec.Hash != it.Hash
		if !changed && !rec.FailedAt.IsZero() && now.Sub(rec.FailedAt) >= hostSyncRetryFailed {
			changed = true
		}
		switch {
		case !changed:
			p.Keep = append(p.Keep, hostSyncStateLine(it.Kind, it.Name, rec))
		case it.Kind == "skill":
			p.Skills = append(p.Skills, it)
		default:
			p.Plugins = append(p.Plugins, it)
		}
	}
	var gone []string
	for k := range st.Items {
		if !want[k] {
			gone = append(gone, k)
		}
	}
	sort.Strings(gone)
	for _, k := range gone {
		kind, name, _ := strings.Cut(k, " ")
		switch {
		case kind == "skill" && skillNameRe.MatchString(name) && name != "synced":
			p.RemovedSkills = append(p.RemovedSkills, name)
		case kind == "plugin" && pluginIDRe.MatchString(name) && st.Items[k].FailedAt.IsZero():
			p.RemovedPlugins = append(p.RemovedPlugins, name)
		}
	}
	return p
}

func hostSyncStateLine(kind, name string, rec hostSyncRecord) string {
	if !rec.FailedAt.IsZero() {
		return fmt.Sprintf("%s %s %s failed %d", kind, name, rec.Hash, rec.FailedAt.Unix())
	}
	return fmt.Sprintf("%s %s %s", kind, name, rec.Hash)
}

// HostSyncResult says what a sync did.
type HostSyncResult struct {
	// UpToDate means the host already matched and nothing was sent.
	UpToDate       bool
	Skills         []string
	RemovedSkills  []string
	Setups         []string
	Plugins        []string
	PluginFailures []string
	RemovedPlugins []string
	// Partial means rsync reported files it could not copy.
	Partial bool
	Skipped []string
}

// Summary is one line for a task log or the CLI.
func (r HostSyncResult) Summary(host string) string {
	if r.UpToDate {
		return fmt.Sprintf("Skills and plugins on %s are up to date", host)
	}
	var parts []string
	if n := len(r.Skills); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skill(s) copied", n))
	}
	if n := len(r.RemovedSkills); n > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", n))
	}
	if n := len(r.Setups); n > 0 {
		parts = append(parts, "setup started for "+strings.Join(r.Setups, ", "))
	}
	if n := len(r.Plugins); n > 0 {
		parts = append(parts, fmt.Sprintf("%d plugin(s) installed", n))
	}
	if n := len(r.RemovedPlugins); n > 0 {
		parts = append(parts, fmt.Sprintf("%d plugin(s) disabled", n))
	}
	if n := len(r.PluginFailures); n > 0 {
		parts = append(parts, fmt.Sprintf("%d plugin(s) failed: %s", n, strings.Join(r.PluginFailures, "; ")))
	}
	if r.Partial {
		parts = append(parts, "some files could not be copied")
	}
	if len(parts) == 0 {
		parts = append(parts, "record refreshed")
	}
	return fmt.Sprintf("Synced skills and plugins to %s: %s", host, strings.Join(parts, ", "))
}

// syncTransport is how a sync reaches a host. The ssh one is below; tests use a
// directory on this machine.
type syncTransport interface {
	// Run runs a POSIX shell script on the host in a login shell.
	Run(ctx context.Context, script string) (string, error)
	// Rsync copies the named entries of srcRoot into dest on the host,
	// following symlinks and deleting what the source no longer has.
	// partial reports files rsync could not copy (the rest were).
	Rsync(ctx context.Context, srcRoot string, names, excludes []string, dest string) (partial bool, err error)
}

// hostSyncer runs syncs, one at a time per host.
type hostSyncer struct {
	mu    sync.Mutex
	hosts map[string]*sync.Mutex

	cacheMu  sync.Mutex
	cached   hostManifest
	cachedAt time.Time
	cacheDir string

	// sourceDir is this machine's Claude config dir. Tests replace it.
	sourceDir func() string
	// transport reaches a host. Tests replace it.
	transport func(host string) syncTransport
}

func newHostSyncer() *hostSyncer {
	return &hostSyncer{
		hosts:     map[string]*sync.Mutex{},
		sourceDir: DefaultClaudeConfigDir,
		transport: func(host string) syncTransport { return sshSyncTransport{r: RemoteRunner{Host: host}} },
	}
}

// hostSyncerFor returns the executor's syncer.
func (e *Executor) hostSyncerFor() *hostSyncer {
	e.hostSyncOnce.Do(func() { e.hostSync = newHostSyncer() })
	return e.hostSync
}

func (s *hostSyncer) lock(host string) func() {
	s.mu.Lock()
	m, ok := s.hosts[host]
	if !ok {
		m = &sync.Mutex{}
		s.hosts[host] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *hostSyncer) manifest() (hostManifest, error) {
	dir := s.sourceDir()
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cacheDir == dir && time.Since(s.cachedAt) < hostSyncManifestTTL {
		return s.cached, nil
	}
	m, err := buildHostManifest(dir)
	if err != nil {
		return m, err
	}
	s.cached, s.cachedAt, s.cacheDir = m, time.Now(), dir
	return m, nil
}

// hostSyncCheckScript prints the host's Claude config dir and ty's record.
const hostSyncCheckScript = `printf 'dir %s\n' "${CLAUDE_CONFIG_DIR:-$HOME/.claude}"; cat "` + hostSyncStateDir + `/state" 2>/dev/null; exit 0`

// Sync brings host up to date with this machine. force ignores the host's
// record and redoes everything.
func (s *hostSyncer) Sync(ctx context.Context, host string, force bool) (HostSyncResult, error) {
	unlock := s.lock(host)
	defer unlock()

	m, err := s.manifest()
	if err != nil {
		return HostSyncResult{}, fmt.Errorf("read this machine's skills: %w", err)
	}
	res := HostSyncResult{Skipped: m.Skipped}
	tr := s.transport(host)

	checkCtx, cancel := context.WithTimeout(ctx, hostSyncCheckTimeout)
	out, err := tr.Run(checkCtx, hostSyncCheckScript)
	cancel()
	if err != nil {
		return res, fmt.Errorf("check %s: %w", host, err)
	}
	dir, st := parseHostSyncState(out)
	if !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, " \t'\"\\$`") {
		return res, fmt.Errorf("%s reported an unusable Claude config dir %q", host, dir)
	}
	if !force && st.Target == dir && st.Manifest == m.Hash {
		res.UpToDate = true
		return res, nil
	}
	plan := planHostSync(m, st, dir, time.Now(), force)
	if plan.empty() && !force {
		// Every item is in place, or waiting out a failed install's retry delay.
		res.UpToDate = true
		return res, nil
	}

	// Skills first, by rsync. Only a transport failure stops here: a skill that
	// did not copy is simply not recorded, and is tried again next time.
	copied := map[string]bool{}
	if len(plan.Skills) > 0 {
		names := make([]string, 0, len(plan.Skills))
		var excludes []string
		for _, sk := range plan.Skills {
			names = append(names, sk.Name)
			excludes = append(excludes, sk.Excludes...)
		}
		partial, err := tr.Rsync(ctx, filepath.Join(s.sourceDir(), "skills"), names, excludes, dir+"/skills")
		if err != nil {
			return res, fmt.Errorf("copy skills to %s: %w", host, err)
		}
		res.Partial = partial
		for _, sk := range plan.Skills {
			copied[sk.Name] = true
			res.Skills = append(res.Skills, sk.Name)
			if sk.Setup {
				res.Setups = append(res.Setups, sk.Name)
			}
		}
	}

	script := hostSyncApplyScript(m, plan, dir, copied)
	out, err = tr.Run(ctx, script)
	if err != nil {
		return res, fmt.Errorf("apply on %s: %w (%s)", host, err, lastLine(out))
	}
	res.RemovedSkills = plan.RemovedSkills
	res.RemovedPlugins = plan.RemovedPlugins
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), " ", 3)
		switch {
		case len(f) >= 2 && f[0] == "plugin-ok":
			res.Plugins = append(res.Plugins, f[1])
		case len(f) >= 2 && f[0] == "plugin-fail":
			why := "failed"
			if len(f) == 3 && strings.TrimSpace(f[2]) != "" {
				why = strings.TrimSpace(f[2])
			}
			res.PluginFailures = append(res.PluginFailures, f[1]+": "+why)
		}
	}
	if !strings.Contains(out, "ty-sync-applied") {
		return res, fmt.Errorf("apply on %s did not finish (%s)", host, lastLine(out))
	}
	return res, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// hostSyncApplyScript renders what the host runs after the skills are copied:
// remove skills ty put there that this machine no longer has, record the
// skills, start setup scripts, install plugins, and record the result.
//
// The record is written twice: once before the plugins, so a slow install cut
// short does not also lose the (already copied) skills, and once at the end.
// A plugin that fails is recorded as failed, with the time, so it is retried a
// day later rather than on every spawn.
func hostSyncApplyScript(m hostManifest, p hostSyncPlan, dir string, copied map[string]bool) string {
	q := shellQuote
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("set -u")
	w("dir=%s", q(dir))
	w("state=\"%s\"", hostSyncStateDir)
	w(`mkdir -p "$state" || exit 1`)
	for _, name := range p.RemovedSkills {
		w(`rm -rf "$dir/skills/"%s`, q(name))
	}

	// The record so far: what carries over, plus the skills just copied.
	base := []string{"target " + dir}
	base = append(base, p.Keep...)
	for _, sk := range p.Skills {
		if copied[sk.Name] {
			base = append(base, fmt.Sprintf("skill %s %s", sk.Name, sk.Hash))
		}
	}
	w(`cat >"$state/.state.tmp" <<'TY_SYNC_STATE'`)
	for _, l := range base {
		w("%s", l)
	}
	w("TY_SYNC_STATE")
	w(`mv -f "$state/.state.tmp" "$state/state"`)

	// Setup scripts run detached and bounded: a skill's own build (gstack
	// compiles its browser and fetches Chromium) can take minutes, and the spawn
	// must not wait on it. Its output is kept for whoever wonders later.
	for _, sk := range p.Skills {
		if !sk.Setup || !copied[sk.Name] {
			continue
		}
		w(`if command -v timeout >/dev/null 2>&1; then bound="timeout 1800"; else bound=""; fi`)
		w(`( cd "$dir/skills/"%s && nohup $bound ./setup </dev/null >"$state/setup-"%s".log" 2>&1 & )`, q(sk.Name), q(sk.Name))
	}

	// Plugins, installed from their marketplaces by the host's own claude.
	w(`cp "$state/state" "$state/.state.tmp"`)
	w("failed=0")
	if len(p.Plugins)+len(p.RemovedPlugins) > 0 {
		w(`if command -v claude >/dev/null 2>&1; then have=1; else have=0; fi`)
		// A private marketplace the host has no credentials for must fail, not
		// wait at a password prompt nobody can see; and no one install may take
		// the whole sync's time.
		w(`export GIT_TERMINAL_PROMPT=0`)
		w(`if command -v timeout >/dev/null 2>&1; then limit="timeout 300"; else limit=""; fi`)
		markets := map[string]bool{}
		for _, pl := range p.Plugins {
			if markets[pl.Marketplace] {
				continue
			}
			markets[pl.Marketplace] = true
			w(`[ "$have" = 1 ] && { $limit claude plugin marketplace add %s </dev/null >/dev/null 2>&1 || $limit claude plugin marketplace update %s </dev/null >/dev/null 2>&1 || true; }`,
				q(pl.Source), q(pl.Marketplace))
		}
		for _, pl := range p.Plugins {
			w(`if [ "$have" = 1 ] && { out=$($limit claude plugin install %[1]s </dev/null 2>&1) || out=$($limit claude plugin update %[1]s </dev/null 2>&1); }; then`, q(pl.Name))
			w(`  $limit claude plugin enable %s </dev/null >/dev/null 2>&1 || true`, q(pl.Name))
			w(`  echo "plugin %s %s" >>"$state/.state.tmp"`, pl.Name, pl.Hash)
			w(`  echo "plugin-ok %s"`, pl.Name)
			w(`else`)
			w(`  [ "$have" = 1 ] || out="claude is not installed there"`)
			w(`  failed=1`)
			w(`  echo "plugin %s %s failed $(date +%%s)" >>"$state/.state.tmp"`, pl.Name, pl.Hash)
			w(`  echo "plugin-fail %s $(printf '%%s' "$out" | tail -n 1 | tr -d '\r' | cut -c1-160)"`, pl.Name)
			w(`fi`)
		}
		for _, id := range p.RemovedPlugins {
			w(`[ "$have" = 1 ] && $limit claude plugin disable %s </dev/null >/dev/null 2>&1`, q(id))
		}
	}
	// Everything recorded and nothing failed: the next check is one comparison.
	complete := true
	for _, sk := range p.Skills {
		if !copied[sk.Name] {
			complete = false
		}
	}
	for _, l := range p.Keep {
		if strings.Contains(l, " failed ") {
			complete = false
		}
	}
	if complete {
		w(`[ "$failed" = 1 ] || echo "manifest %s" >>"$state/.state.tmp"`, m.Hash)
	}
	w(`mv -f "$state/.state.tmp" "$state/state"`)
	w(`echo ty-sync-applied`)
	return b.String()
}

// sshSyncTransport reaches a host over ssh, through the same multiplexed
// connection every other remote command uses.
type sshSyncTransport struct{ r RemoteRunner }

func (t sshSyncTransport) Run(ctx context.Context, script string) (string, error) {
	cmd := t.r.Command(ctx, "", "sh", "-c", script)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = lastLine(stdout.String())
		}
		return stdout.String(), fmt.Errorf("%w (%s)", err, detail)
	}
	return stdout.String(), nil
}

func (t sshSyncTransport) Rsync(ctx context.Context, srcRoot string, names, excludes []string, dest string) (bool, error) {
	sshCmd := []string{t.r.ssh()}
	sshCmd = append(sshCmd, t.r.sshOptions()...)
	return runRsync(ctx, srcRoot, names, excludes, t.r.Host+":"+dest+"/",
		"-e", strings.Join(sshCmd, " "),
		// The host's skills directory may not exist yet, and the rsync on this
		// Mac is too old for --mkpath.
		"--rsync-path=mkdir -p "+shellQuote(dest)+" && rsync")
}

// runRsync copies names (entries of srcRoot) into target — the same copy
// whether target is a host or, in tests, a directory here.
func runRsync(ctx context.Context, srcRoot string, names, excludes []string, target string, extra ...string) (bool, error) {
	ex, err := os.CreateTemp("", "ty-sync-exclude-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(ex.Name())
	for _, pattern := range append(syncExcludeRules(), excludes...) {
		fmt.Fprintln(ex, pattern)
	}
	if err := ex.Close(); err != nil {
		return false, err
	}
	args := append([]string{"-a", "-L", "--delete", "--exclude-from=" + ex.Name()}, extra...)
	args = append(args, "--")
	args = append(args, names...)
	args = append(args, target)
	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Dir = srcRoot
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	return rsyncOutcome(err, out)
}

// syncExcludeRules are the rsync rules every skill copy carries.
func syncExcludeRules() []string {
	rules := []string{".DS_Store"}
	for d := range syncSkipDirs {
		rules = append(rules, d+"/")
	}
	sort.Strings(rules)
	return rules
}

// rsyncOutcome reads rsync's exit: 23 and 24 are a partial copy — some files
// could not be read, or vanished — which is recorded as done rather than
// retried on every spawn; anything else is a failure.
func rsyncOutcome(err error, out []byte) (bool, error) {
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() == 23 || exitErr.ExitCode() == 24) {
		return true, nil
	}
	return false, fmt.Errorf("rsync: %w (%s)", err, lastLine(string(out)))
}

// syncHostTools brings a placed host's skills and plugins in step with this
// machine's before a task spawns there. It never fails the spawn: a problem is
// logged and the task launches with whatever the host has. A sync that takes
// longer than hostSyncWait carries on in the background.
func (e *Executor) syncHostTools(ctx context.Context, task *db.Task, host string) {
	s := e.hostSyncerFor()
	type outcome struct {
		res HostSyncResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		// Not the spawn's context: a sync that outlasts the wait carries on after
		// the task has started, and is bounded by its own timeout instead.
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hostSyncTimeout)
		defer cancel()
		res, err := s.Sync(syncCtx, host, false)
		done <- outcome{res, err}
	}()

	report := func(o outcome) {
		switch {
		case o.err != nil:
			e.logger.Warn("host sync failed", "task", task.ID, "host", host, "error", o.err)
			e.logLine(task.ID, "system", fmt.Sprintf(
				"Could not bring skills and plugins on %s up to date (%v); launching with what it has.", host, o.err))
		case !o.res.UpToDate:
			e.logLine(task.ID, "system", o.res.Summary(host))
		}
	}

	wait := time.NewTimer(hostSyncWaitFor())
	defer wait.Stop()
	select {
	case o := <-done:
		report(o)
	case <-wait.C:
		e.logLine(task.ID, "system", fmt.Sprintf(
			"Still copying skills and plugins to %s; starting without waiting for it to finish.", host))
		go func() { report(<-done) }()
	case <-ctx.Done():
		go func() { report(<-done) }()
	}
}

// hostSyncWaitFor is hostSyncWait, shortened by tests.
var hostSyncWaitFor = func() time.Duration { return hostSyncWait }

// SyncHost is the manual trigger (`ty hosts sync`): the same sync a spawn runs,
// reported to the caller rather than to a task.
func SyncHost(ctx context.Context, host string, force bool) (HostSyncResult, error) {
	if strings.TrimSpace(host) == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t\r\n") {
		return HostSyncResult{}, fmt.Errorf("%q is not an ssh destination", host)
	}
	return newHostSyncer().Sync(ctx, host, force)
}
