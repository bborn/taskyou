package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// controlEntry is the per-uid directory name sshControlDir creates under its
// chosen base. It mirrors the literal in prepareSSHControlDir so a test can
// plant a fixture (a symlink, a wrong-perms dir, a foreign-owned dir) at the
// exact path production will inspect, then assert the outcome.
func controlEntry() string {
	return fmt.Sprintf("ty-ssh-%d", os.Getuid())
}

// The /tmp fallback target a successful fallback resolves to. Tests assert
// against this exact path so a regression that returns the planted symlink
// path (the original bug) is caught as a mismatch rather than a silent pass.
func controlEntryInTmp() string {
	return filepath.Join("/tmp", controlEntry())
}

// sshControlDirCandidates prefers an absolute $XDG_RUNTIME_DIR over /tmp, in
// that order: an XDG_RUNTIME_DIR tmpfs is per-uid 0700 by spec, so the symlink
// squat the /tmp fallback is hardened against is not even possible there.
func TestSSHControlDirCandidatesPrefersAbsoluteXDGOverTmp(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/12345")
	got := sshControlDirCandidates()
	if len(got) != 2 || got[0] != "/run/user/12345" || got[1] != "/tmp" {
		t.Fatalf("candidates = %v, want [/run/user/12345, /tmp]", got)
	}
}

// A relative XDG_RUNTIME_DIR violates the XDG spec (which requires an absolute
// path) and would create the dir in an unpredictable cwd, so it is ignored and
// the /tmp fallback is used alone.
func TestSSHControlDirCandidatesIgnoresRelativeXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "relative/path")
	got := sshControlDirCandidates()
	if len(got) != 1 || got[0] != "/tmp" {
		t.Fatalf("candidates = %v, want [/tmp] (relative XDG_RUNTIME_DIR ignored)", got)
	}
}

// An empty/unset XDG_RUNTIME_DIR is treated as absent: /tmp is the sole
// candidate, preserving pre-hardening behaviour.
func TestSSHControlDirCandidatesFallsBackToTmpWhenXDGEmpty(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := sshControlDirCandidates()
	if len(got) != 1 || got[0] != "/tmp" {
		t.Fatalf("candidates = %v, want [/tmp] when XDG_RUNTIME_DIR is empty", got)
	}
}

// A clean, victim-owned, 0700 directory is the happy path: prepareSSHControlDir
// returns the literal directory path, which is what feeds ControlPath.
func TestPrepareSSHControlDirAcceptsCleanPrivateDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, controlEntry())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := prepareSSHControlDir(base); got != dir {
		t.Fatalf("prepareSSHControlDir(%q) = %q, want %q", base, got, dir)
	}
}

// THE BUG: a symlink planted at the control-dir entry by another user resolves,
// via os.Stat, to a victim-owned 0700 directory and passes the perm/owner
// check, while the function returns the literal symlink path the attacker can
// re-point. Lstat inspects the literal entry and rejects the symlink outright;
// os.MkdirAll does not clear it (it follows the symlink and no-ops when the
// target exists), so the Lstat check is load-bearing.
func TestPrepareSSHControlDirRejectsSymlinkPlantedAtEntry(t *testing.T) {
	base := t.TempDir()
	// A victim-owned 0700 directory the planted symlink resolves to.
	target := filepath.Join(base, "victim-owned-0700")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(base, controlEntry())
	if err := os.Symlink(target, entry); err != nil {
		t.Fatal(err)
	}
	got := prepareSSHControlDir(base)
	if got != "" {
		// Also assert the literal symlink path is NOT returned: returning it
		// would hand the attacker an entry he can re-point between the check
		// and ssh's connect(2).
		t.Fatalf("prepareSSHControlDir returned %q for a symlinked entry; want \"\" — a symlink another user planted at the control dir must not be used as the ControlPath, since it can be re-pointed to MITM ssh", got)
	}
}

// A symlink whose target does NOT yet exist must also be rejected on the
// literal entry, not on the resolved (missing) target.
func TestPrepareSSHControlDirRejectsDanglingSymlinkAtEntry(t *testing.T) {
	base := t.TempDir()
	entry := filepath.Join(base, controlEntry())
	// Point at a target that does not exist; MkdirAll would follow the symlink
	// and try to create the target. The literal entry remains a symlink.
	if err := os.Symlink(filepath.Join(base, "does-not-exist"), entry); err != nil {
		t.Fatal(err)
	}
	if got := prepareSSHControlDir(base); got != "" {
		t.Fatalf("prepareSSHControlDir returned %q for a dangling symlink; want \"\"", got)
	}
}

// A real directory (not a symlink) but with group/world bits set leaks the
// control socket and must be rejected.
func TestPrepareSSHControlDirRejectsLoosePermissions(t *testing.T) {
	base := t.TempDir()
	entry := filepath.Join(base, controlEntry())
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	// MkdirAll on an existing dir does not tighten its perms, so the 0o755
	// survives and the perm check must catch it.
	if got := prepareSSHControlDir(base); got != "" {
		t.Fatalf("prepareSSHControlDir returned %q for a 0o755 dir; want \"\" — a group/world-readable control dir leaks the socket", got)
	}
}

// A real, 0700 directory owned by another user is not somewhere the current
// uid should place a control socket. Requires root to chown; skipped otherwise
// (the symlink vector — the actual reported bug — needs no root: see above).
func TestPrepareSSHControlDirRejectsForeignOwnedDir(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chowning to a different uid requires root; the reported symlink vector is covered by the non-root tests above")
	}
	base := t.TempDir()
	entry := filepath.Join(base, controlEntry())
	if err := os.MkdirAll(entry, 0o700); err != nil {
		t.Fatal(err)
	}
	other := os.Getuid() + 1 // any uid that is not the current one
	if err := os.Chown(entry, other, os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if got := prepareSSHControlDir(base); got != "" {
		t.Fatalf("prepareSSHControlDir returned %q for a dir owned by uid %d; want \"\" — only a dir the current uid owns may hold a control socket", got, other)
	}
}

// A non-directory entry (a plain file) at the path must be rejected; ssh needs
// a directory to place a unix socket in.
func TestPrepareSSHControlDirRejectsRegularFileAtEntry(t *testing.T) {
	base := t.TempDir()
	entry := filepath.Join(base, controlEntry())
	if err := os.WriteFile(entry, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := prepareSSHControlDir(base); got != "" {
		t.Fatalf("prepareSSHControlDir returned %q for a regular file; want \"\"", got)
	}
}

// End-to-end: when $XDG_RUNTIME_DIR is set to a writable directory,
// sshControlDir uses it in preference to /tmp, so the /tmp fallback is not
// touched and no shared /tmp state is created.
func TestSSHControlDirPrefersXDGWhenSet(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	got := sshControlDir()
	want := filepath.Join(xdg, controlEntry())
	if got != want {
		t.Fatalf("sshControlDir() = %q, want the XDG_RUNTIME_DIR entry %q", got, want)
	}
}

// End-to-end: when the XDG entry is a symlink (the attack), sshControlDir
// rejects it and falls back to /tmp rather than returning the symlink path.
// Exercises both the rejection and the candidate-loop fallback.
func TestSSHControlDirFallsBackToTmpWhenXDGEntryIsASymlink(t *testing.T) {
	xdg := t.TempDir()
	target := filepath.Join(xdg, "victim-owned-0700")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(xdg, controlEntry())); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", xdg)

	// The fallback creates /tmp/ty-ssh-<uid>; clean it up so this test does
	// not leave shared state for later tests in this package. Tests here run
	// sequentially (none call t.Parallel), so removal is race-free.
	t.Cleanup(func() { _ = os.RemoveAll(controlEntryInTmp()) })

	got := sshControlDir()
	want := controlEntryInTmp()
	if got != want {
		t.Fatalf("sshControlDir() = %q, want the /tmp fallback %q after the XDG entry was rejected as a symlink", got, want)
	}
}

// The ControlPath ssh actually receives must point inside the validated
// control dir, not at a symlinked entry an attacker can re-point.
func TestSSHMultiplexArgsControlPathLivesUnderTheControlDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	args := strings.Join(sshMultiplexArgs(), "\x00")
	dir := filepath.Join(xdg, controlEntry())
	want := "ControlPath=" + filepath.Join(dir, "%C")
	if !strings.Contains(args, want) {
		t.Fatalf("multiplex args %q do not carry %q", args, want)
	}
	for _, want := range []string{"ControlMaster=auto", "ControlPath=", "ControlPersist="} {
		if !strings.Contains(args, want) {
			t.Errorf("multiplex args %q missing %q", args, want)
		}
	}
}

// When no control dir can be prepared, sshMultiplexArgs returns nil so ssh
// silently falls back to a normal, non-multiplexed connection — fail secure,
// never fall back to an untrusted dir. Drives the rejection with both
// candidates unusable: XDG entry is a regular file (MkdirAll -> ENOTDIR) and
// /tmp's entry is also a regular file. Cleanup restores the path so later
// tests in this package that rely on /tmp/ty-ssh-<uid> recreating as a real
// dir are unaffected (tests here run sequentially — none call t.Parallel).
func TestSSHMultiplexArgsOmittedWhenNoControlDirCanBePrepared(t *testing.T) {
	// XDG candidate: a regular file at the entry makes MkdirAll fail (ENOTDIR).
	xdg := t.TempDir()
	if err := os.WriteFile(filepath.Join(xdg, controlEntry()), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", xdg)

	// /tmp candidate: a regular file at the entry, likewise.
	tmpEntry := controlEntryInTmp()
	_ = os.RemoveAll(tmpEntry)
	if err := os.WriteFile(tmpEntry, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(tmpEntry) })

	if got := sshMultiplexArgs(); got != nil {
		t.Fatalf("sshMultiplexArgs() = %v, want nil when no candidate prepares a trusted dir (fail secure, do not fall back to an untrusted path)", got)
	}
}
