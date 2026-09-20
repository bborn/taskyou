package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/registry"
)

// This file is the *install* half of plugin management, next to the load half in
// plugins.go. It lives here — below internal/ui and free of cobra — so the CLI,
// the TUI browser, and the HTTP API all install a plugin the same way instead of
// each shelling out to `ty plugins add`.

// sourcesFile records, per installed directory, where that plugin came from. It
// is what lets `ty plugins update` re-pull a plugin that was installed out of a
// subdirectory of a collection repo (where the install is a copy, not a
// checkout, so there is no git remote to ask).
//
// It sits beside the plugin directories rather than inside them: a marker file
// inside a git checkout would show up as an uncommitted change forever.
const sourcesFile = ".sources.json"

// InstalledSource is one directory's provenance.
type InstalledSource struct {
	// ID is the catalog entry this came from, when it was installed by ID.
	ID string `json:"id,omitempty"`
	// Source is the git URL (or local path) it was cloned from.
	Source string `json:"source"`
	// Subdir is the path inside Source that was copied out, if any.
	Subdir string `json:"subdir,omitempty"`
	// InstalledAt is an RFC3339 timestamp of the last install or update.
	InstalledAt string `json:"installed_at,omitempty"`
}

// LoadSources returns the provenance records for pluginsDir, keyed by the
// installed directory's base name. A missing or unreadable file yields an empty
// map — provenance is a convenience, never a precondition.
func LoadSources(pluginsDir string) map[string]InstalledSource {
	out := map[string]InstalledSource{}
	if pluginsDir == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(pluginsDir, sourcesFile))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]InstalledSource{}
	}
	return out
}

// saveSource upserts one provenance record.
func saveSource(pluginsDir, dirName string, src InstalledSource) error {
	all := LoadSources(pluginsDir)
	all[dirName] = src
	return writeSources(pluginsDir, all)
}

// dropSource removes one provenance record.
func dropSource(pluginsDir, dirName string) {
	all := LoadSources(pluginsDir)
	if _, ok := all[dirName]; !ok {
		return
	}
	delete(all, dirName)
	_ = writeSources(pluginsDir, all)
}

func writeSources(pluginsDir string, all map[string]InstalledSource) error {
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(pluginsDir, sourcesFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// InstallRequest says what to install. It is the resolved form — Resolve turns
// whatever the user typed into one of these.
type InstallRequest struct {
	// ID is the catalog entry ID, when the install came from the catalog.
	ID string
	// Source is the git URL or local path to clone.
	Source string
	// Subdir is the plugin's path inside Source. When set, only that
	// subdirectory is installed, so a user can take one plugin out of a
	// collection instead of all of them.
	Subdir string
	// Name overrides the directory name to install under.
	Name string
	// Entry is the catalog entry this request came from, for display.
	Entry *registry.Entry
}

// InstallResult describes what an install actually did.
type InstallResult struct {
	// Dir is the installed directory.
	Dir string
	// Name is that directory's base name.
	Name string
	// Plugins are the manifest names that became active.
	Plugins []string
	// Updated is true when an existing install was refreshed rather than created.
	Updated bool
	// backup is the path of the prior version set aside during a subdir update.
	// It is retained until Install validates the new copy as a usable plugin and
	// is dropped on success or restored on failure. Empty for fresh installs and
	// whole-repo installs.
	backup string
}

// Verb returns "Installed" or "Updated", for one-line user feedback.
func (r InstallResult) Verb() string {
	if r.Updated {
		return "Updated"
	}
	return "Installed"
}

// Resolve turns what a user typed into an InstallRequest, consulting the catalog
// so a bare handle works: `ty plugins add slack`.
//
// The precedence is unambiguous-first: an explicit URL or an existing path is
// taken literally, and only a bare word is looked up in the catalog. A bare word
// that isn't in the catalog produces an error naming the closest entries, which
// is the whole point of having a catalog — a wrong guess should teach, not just
// fail.
func Resolve(ctx context.Context, arg string, entries []registry.Entry) (InstallRequest, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return InstallRequest{}, fmt.Errorf("nothing to install")
	}

	if looksLikeGitSource(arg) {
		return InstallRequest{Source: normalizeGitSource(arg)}, nil
	}
	if isLocalPluginPath(arg) {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return InstallRequest{}, err
		}
		return InstallRequest{Source: abs}, nil
	}

	if e, ok := registry.Find(entries, arg); ok {
		entry := e
		return InstallRequest{ID: e.ID, Source: e.Source, Subdir: e.Subdir, Name: e.ID, Entry: &entry}, nil
	}

	if suggestions := registry.DidYouMean(entries, arg, 3); len(suggestions) > 0 {
		return InstallRequest{}, fmt.Errorf("no plugin %q in the catalog — did you mean %s? (run `ty plugins search %s`)",
			arg, strings.Join(suggestions, ", "), arg)
	}
	return InstallRequest{}, fmt.Errorf("no plugin %q in the catalog, and it is not a git URL or a local path "+
		"(run `ty plugins browse` to see what's available)", arg)
}

// looksLikeGitSource reports whether arg is meant as a repository rather than a
// catalog handle. The owner/repo shorthand counts: `ty plugins add taskyou/plugins`
// should not go hunting in the catalog for a plugin with a slash in its name.
func looksLikeGitSource(arg string) bool {
	switch {
	case strings.Contains(arg, "://"), strings.HasPrefix(arg, "git@"):
		return true
	case strings.HasPrefix(arg, "github.com/"):
		return true
	case strings.HasPrefix(arg, "./"), strings.HasPrefix(arg, "../"), strings.HasPrefix(arg, "/"), strings.HasPrefix(arg, "~"):
		return false // a path, handled by isLocalPluginPath
	case strings.Count(arg, "/") == 1 && !strings.Contains(arg, " "):
		// owner/repo — but only if it isn't an existing relative path.
		return !dirExists(arg)
	}
	return false
}

// normalizeGitSource expands the shorthands looksLikeGitSource accepts into a
// clonable URL.
func normalizeGitSource(arg string) string {
	switch {
	case strings.Contains(arg, "://"), strings.HasPrefix(arg, "git@"):
		return arg
	case strings.HasPrefix(arg, "github.com/"):
		return "https://" + arg
	case strings.Count(arg, "/") == 1:
		return "https://github.com/" + arg
	}
	return arg
}

func isLocalPluginPath(arg string) bool {
	if strings.HasPrefix(arg, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			arg = filepath.Join(home, strings.TrimPrefix(arg, "~"))
		}
	}
	return dirExists(arg)
}

// Install installs (or updates) a plugin into pluginsDir.
//
// Two shapes, because collection repos are the norm: with no Subdir the source is
// cloned straight into pluginsDir/<name> and every plugin inside becomes active
// (and a re-install is a `git pull`); with a Subdir the source is cloned to a
// staging dir and only that subdirectory is copied in, so one plugin can be taken
// out of a collection of ten.
//
// A fresh install that turns out not to contain a usable plugin is rolled back,
// so a mistyped URL never leaves a stray directory behind.
func Install(ctx context.Context, pluginsDir string, req InstallRequest) (InstallResult, error) {
	if pluginsDir == "" {
		return InstallResult{}, fmt.Errorf("no plugins directory configured")
	}
	if req.Source == "" {
		return InstallResult{}, fmt.Errorf("no source to install from")
	}
	name := req.Name
	if name == "" {
		name = deriveInstallName(req.Source, req.Subdir)
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return InstallResult{}, fmt.Errorf("could not derive a safe plugin directory name from %q; pass a name", req.Source)
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create plugins dir: %w", err)
	}

	target := filepath.Join(pluginsDir, name)
	var res InstallResult
	var err error
	if req.Subdir != "" {
		res, err = installSubdir(ctx, pluginsDir, target, req)
	} else {
		res, err = installWholeRepo(ctx, target, req)
	}
	if err != nil {
		return InstallResult{}, err
	}

	res.Name = name
	res.Plugins = installedPluginNames(pluginsDir, target)
	if len(res.Plugins) == 0 {
		if !res.Updated {
			_ = os.RemoveAll(target)
		} else if res.backup != "" {
			// A filesystem-successful copy replaced a working plugin but loads no
			// usable plugins — the upstream release was semantically broken. Roll
			// the working version back from the backup rather than leaving the
			// broken copy in place with no automatic recovery.
			_ = os.RemoveAll(target)
			_ = os.Rename(res.backup, target)
		}
		return InstallResult{}, fmt.Errorf("%s contains no usable plugins (need a %s with a hook, action, workflow, service, or routine)",
			describeSource(req), ManifestName)
	}
	// The new copy is validated as usable; the backup of the previous version is
	// now safe to drop.
	if res.backup != "" {
		_ = os.RemoveAll(res.backup)
	}
	_ = saveSource(pluginsDir, name, InstalledSource{
		ID:          req.ID,
		Source:      req.Source,
		Subdir:      req.Subdir,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	})
	return res, nil
}

func describeSource(req InstallRequest) string {
	if req.Subdir != "" {
		return req.Source + "/" + req.Subdir
	}
	return req.Source
}

// installWholeRepo clones the source into target, or pulls if it is already a
// checkout there.
func installWholeRepo(ctx context.Context, target string, req InstallRequest) (InstallResult, error) {
	switch {
	case isGitCheckout(target):
		if out, err := runGit(ctx, target, "pull", "--ff-only"); err != nil {
			return InstallResult{}, fmt.Errorf("update %s: %w\n%s", filepath.Base(target), err, out)
		}
		return InstallResult{Dir: target, Updated: true}, nil
	case dirExists(target):
		return InstallResult{}, fmt.Errorf("%s already exists and is not a git checkout; remove it or install under a different name", target)
	default:
		if out, err := runGit(ctx, "", "clone", "--depth", "1", req.Source, target); err != nil {
			return InstallResult{}, fmt.Errorf("clone %s: %w\n%s", req.Source, err, out)
		}
		return InstallResult{Dir: target}, nil
	}
}

// installSubdir clones the source to a staging dir and copies one subdirectory
// into place. Copy, not checkout: the installed plugin is a plain directory, so
// nothing about it depends on the collection repo staying around.
func installSubdir(ctx context.Context, pluginsDir, target string, req InstallRequest) (InstallResult, error) {
	if err := safeSubdir(req.Subdir); err != nil {
		return InstallResult{}, err
	}
	staging, err := os.MkdirTemp("", "ty-plugin-*")
	if err != nil {
		return InstallResult{}, fmt.Errorf("staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	checkout := filepath.Join(staging, "repo")
	if out, gerr := runGit(ctx, "", "clone", "--depth", "1", req.Source, checkout); gerr != nil {
		return InstallResult{}, fmt.Errorf("clone %s: %w\n%s", req.Source, gerr, out)
	}
	src := filepath.Join(checkout, filepath.FromSlash(req.Subdir))
	if _, serr := os.Stat(filepath.Join(src, ManifestName)); serr != nil {
		return InstallResult{}, fmt.Errorf("%s has no %s in %s", req.Source, ManifestName, req.Subdir)
	}

	updated := dirExists(target)
	var backup string
	if updated {
		// Move the old copy aside and put the new one in. The old copy is kept as a
		// backup until Install validates the new copy as a usable plugin, so a
		// copy that succeeds on the filesystem but turns out to be a semantically
		// broken upstream (no loadable plugin) is rolled back rather than
		// destroying a working plugin where a working one used to be.
		backup = target + ".old"
		_ = os.RemoveAll(backup)
		if rerr := os.Rename(target, backup); rerr != nil {
			return InstallResult{}, fmt.Errorf("replace %s: %w", target, rerr)
		}
		if cerr := copyTree(src, target); cerr != nil {
			_ = os.RemoveAll(target)
			_ = os.Rename(backup, target)
			return InstallResult{}, fmt.Errorf("install %s: %w", req.Subdir, cerr)
		}
	} else if cerr := copyTree(src, target); cerr != nil {
		_ = os.RemoveAll(target)
		return InstallResult{}, fmt.Errorf("install %s: %w", req.Subdir, cerr)
	}
	return InstallResult{Dir: target, Updated: updated, backup: backup}, nil
}

// safeSubdir rejects a subdir that could escape the checkout.
func safeSubdir(subdir string) error {
	clean := filepath.Clean(filepath.FromSlash(subdir))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("unsafe plugin subdirectory %q", subdir)
	}
	return nil
}

// Update re-installs an already-installed plugin from wherever it came from: a
// `git pull` for a checkout, a re-copy from the collection repo for a subdir
// install. Name is the manifest name or the directory name, as `plugins list`
// shows it.
//
// The unit of update is the *install root*, not the plugin: one `plugins add` of
// a collection repo produces one checkout holding many plugins, and updating any
// of them means pulling that checkout.
func Update(ctx context.Context, pluginsDir, name string) (InstallResult, error) {
	dir, err := resolveInstalledDir(pluginsDir, name)
	if err != nil {
		return InstallResult{}, err
	}
	root, src, ok := InstallRoot(pluginsDir, dir)
	if !ok {
		return InstallResult{}, fmt.Errorf("don't know where %q came from (not a git checkout and no recorded source); reinstall it with `ty plugins add`", name)
	}
	rootName := filepath.Base(root)

	if src.Source != "" {
		return Install(ctx, pluginsDir, InstallRequest{
			ID: src.ID, Source: src.Source, Subdir: src.Subdir, Name: rootName,
		})
	}
	if out, gerr := runGit(ctx, root, "pull", "--ff-only"); gerr != nil {
		return InstallResult{}, fmt.Errorf("update %s: %w\n%s", name, gerr, out)
	}
	return InstallResult{Dir: root, Name: rootName, Updated: true, Plugins: installedPluginNames(pluginsDir, root)}, nil
}

// InstallRoot walks up from an installed plugin's directory to the directory that
// `plugins add` actually created — the plugin's own dir for a single install, the
// shared checkout for a plugin nested in a collection repo — and returns the
// provenance recorded for it, if any.
func InstallRoot(pluginsDir, dir string) (root string, src InstalledSource, ok bool) {
	sources := LoadSources(pluginsDir)
	clean := filepath.Clean(pluginsDir)
	for d := filepath.Clean(dir); strings.HasPrefix(d, clean+string(os.PathSeparator)); d = filepath.Dir(d) {
		if rec, found := sources[filepath.Base(d)]; found && rec.Source != "" {
			return d, rec, true
		}
		if isGitCheckout(d) {
			return d, InstalledSource{}, true
		}
	}
	return "", InstalledSource{}, false
}

// Remove uninstalls the plugin named name by deleting its directory.
//
// The name is matched against the loaded plugins' manifest names (what
// `plugins list` shows), falling back to the directory's base name, so it works
// even when a plugin's directory name differs from its manifest name.
//
// It returns the removed directory and whether that directory sat inside a
// multi-plugin collection checkout — in which case only the plugin's own subdir
// is deleted (leaving the shared git checkout and its siblings), and a later
// install of the source may restore it. The plugin dir is verified to live
// strictly inside pluginsDir before anything is deleted, so a corrupt manifest
// can't point removal at an arbitrary path.
func Remove(name, pluginsDir string) (dir string, inCheckout bool, err error) {
	dir, err = resolveInstalledDir(pluginsDir, name)
	if err != nil {
		return "", false, err
	}
	rel, relErr := filepath.Rel(pluginsDir, dir)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false, fmt.Errorf("refusing to remove %s: not inside plugins dir %s", dir, pluginsDir)
	}
	inCheckout = insideCollectionCheckout(dir, pluginsDir)
	if err := os.RemoveAll(dir); err != nil {
		return "", false, fmt.Errorf("remove %s: %w", dir, err)
	}
	dropSource(pluginsDir, filepath.Base(dir))
	return dir, inCheckout, nil
}

// resolveInstalledDir maps a user-supplied plugin name to its directory.
func resolveInstalledDir(pluginsDir, name string) (string, error) {
	if pluginsDir == "" {
		return "", fmt.Errorf("no plugins directory configured")
	}
	plugins, _ := LoadPlugins(pluginsDir)
	for i := range plugins {
		if plugins[i].Name == name || filepath.Base(plugins[i].Dir) == name {
			return plugins[i].Dir, nil
		}
	}
	return "", fmt.Errorf("no plugin named %q installed in %s; run `ty plugins list` to see what's installed", name, pluginsDir)
}

// insideCollectionCheckout reports whether dir is a plugin nested inside a shared
// git checkout (a collection repo cloned by an install), rather than being its own
// checkout. It's true when dir itself is not a git checkout but an ancestor
// strictly between it and pluginsDir is.
func insideCollectionCheckout(dir, pluginsDir string) bool {
	if isGitCheckout(dir) {
		return false
	}
	for parent := filepath.Dir(dir); len(parent) > len(pluginsDir); parent = filepath.Dir(parent) {
		if isGitCheckout(parent) {
			return true
		}
	}
	return false
}

// deriveInstallName picks the directory name to install under: the subdirectory's
// own name when one plugin is taken out of a collection, otherwise the repo name.
func deriveInstallName(source, subdir string) string {
	if subdir != "" {
		return filepath.Base(filepath.Clean(filepath.FromSlash(subdir)))
	}
	s := strings.TrimSuffix(strings.TrimRight(source, "/"), ".git")
	return filepath.Base(s)
}

// installedPluginNames returns the names of every plugin LoadPlugins discovers at
// or under target — one for a single-plugin repo, several for a collection repo.
// Matched by Dir prefix since a plugin's manifest name may differ from its dir.
func installedPluginNames(pluginsDir, target string) []string {
	plugins, _ := LoadPlugins(pluginsDir)
	var names []string
	for _, p := range plugins {
		if p.Dir == target || strings.HasPrefix(p.Dir, target+string(os.PathSeparator)) {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	return names
}

// IsInstalled reports whether a catalog entry is already installed, and under
// which directory. An entry counts as installed when a directory records it as
// its source, or when a plugin's manifest name or directory name matches the
// entry ID — the last two cover a hand-copied plugin, which should still show as
// installed in the browser rather than inviting a duplicate.
func IsInstalled(pluginsDir, id string, plugins []Plugin) (string, bool) {
	for dirName, src := range LoadSources(pluginsDir) {
		if src.ID != "" && strings.EqualFold(src.ID, id) {
			return dirName, true
		}
	}
	for _, p := range plugins {
		if strings.EqualFold(p.Name, id) || strings.EqualFold(filepath.Base(p.Dir), id) {
			return filepath.Base(p.Dir), true
		}
	}
	return "", false
}

func isGitCheckout(dir string) bool { return dirExists(filepath.Join(dir, ".git")) }

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// runGit runs git with the ambient environment minus anything interactive: a
// clone that pops a credential prompt would hang a TUI install forever.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	return cmd.CombinedOutput()
}

// copyTree copies src to dst recursively, preserving the executable bit (plugin
// scripts are useless without it) and skipping git metadata.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			if d.Name() == ".git" && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			// Symlinks and devices are not something a plugin needs, and copying
			// them blindly is how a tarball escapes its directory.
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		return copyFile(path, filepath.Join(dst, rel), info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
