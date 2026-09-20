package hooks

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServices_StopRaceOnProcs forces the timeout branch of ServiceSet.Stop by
// trapping SIGTERM, so the background Wait-goroutine and the main goroutine's
// snapshot write previously raced on `s.procs`. Under -race this used to fail;
// the fix snapshots the slice so the goroutine never reads the field after the
// foreground nils it. The test also asserts the misbehaving service actually
// gets reaped by the SIGKILL fallback (the whole point of the timeout branch).
func TestServices_StopRaceOnProcs(t *testing.T) {
	root := t.TempDir()
	pidfile := filepath.Join(t.TempDir(), "svc.pid")
	manifest := "name: svc\nservices:\n  - name: beat\n    command: \"trap '' TERM; echo $$ > " + pidfile + "; sleep 60\"\n"
	writePlugin(t, root, "svc", manifest, nil)

	set := StartServices(root, nil, nil)
	if set.Count() != 1 {
		t.Fatalf("Count = %d, want 1", set.Count())
	}

	// Wait for the service to actually be up (it trapped TERM and is sleeping)
	// and capture the shell's pid so we can verify Stop reaps it via SIGKILL.
	var pid int
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(pidfile); err == nil && len(b) > 0 {
			if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
				pid = p
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("service never wrote its pid — it did not start")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("service pid %d not alive after start: %v", pid, err)
	}

	start := time.Now()
	set.Stop()
	elapsed := time.Since(start)

	// Stop must have taken the timeout branch (~5s) since SIGTERM was ignored.
	if elapsed < 4*time.Second {
		t.Fatalf("Stop returned after %v; expected to wait ~5s for the SIGTERM-ignoring service", elapsed)
	}

	// After Stop returns, the misbehaving service must be gone: the SIGKILL
	// fallback is the whole reason the timeout branch exists, and the fix
	// drains the reaping goroutine so reaps complete before Stop returns.
	gone := false
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(pid, 0); err != nil {
			gone = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Errorf("service pid %d still alive after Stop (SIGKILL fallback not effective)", pid)
	}

	if set.Count() != 0 {
		t.Errorf("Count after Stop = %d, want 0 (procs should be cleared)", set.Count())
	}
}
