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

// A plugin that declares only a service (no hooks/actions/workflows) still loads.
func TestService_OnlyPluginIsUsable(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "svc", "name: svc\nservices:\n  - name: beat\n    command: sleep 1\n", nil)

	plugins, warnings := LoadPlugins(root)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(plugins) != 1 || len(plugins[0].Services) != 1 {
		t.Fatalf("service-only plugin not loaded: %+v", plugins)
	}
	if plugins[0].Services[0].Name != "beat" {
		t.Errorf("service name = %q, want beat", plugins[0].Services[0].Name)
	}
}

// A service with no command is dropped as malformed.
func TestService_MalformedDropped(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "svc", "name: svc\nservices:\n  - name: nocmd\nhooks:\n  task.done: d.sh\n",
		map[string]string{"d.sh": "#!/bin/sh\n"})
	plugins, warnings := LoadPlugins(root)
	if len(plugins) != 1 || len(plugins[0].Services) != 0 {
		t.Fatalf("malformed service not dropped: %+v", plugins)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning about the malformed service")
	}
}

// StartServices launches the declared service; Stop terminates it.
func TestServices_StartAndStop(t *testing.T) {
	root := t.TempDir()
	pidfile := filepath.Join(t.TempDir(), "svc.pid")
	manifest := "name: svc\nservices:\n  - name: beat\n    command: \"echo $$ > " + pidfile + "; sleep 30\"\n"
	writePlugin(t, root, "svc", manifest, nil)

	set := StartServices(root, nil, nil)
	t.Cleanup(set.Stop)
	if set.Count() != 1 {
		t.Fatalf("Count = %d, want 1 running service", set.Count())
	}

	// The service should write its pid (proof it actually launched).
	var pid int
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(pidfile); err == nil {
			if pid, _ = strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("service never wrote its pid — it did not start")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("service pid %d not alive after start: %v", pid, err)
	}

	set.Stop()

	gone := false
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(pid, 0); err != nil {
			gone = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Errorf("service pid %d still alive after Stop", pid)
	}
}

// injectEnv passed to StartServices reaches the service process.
func TestServices_InjectEnv(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "env.out")
	manifest := "name: svc\nservices:\n  - name: echo\n    command: \"printf %s \\\"$TY_API_URL\\\" > " + out + "; sleep 30\"\n"
	writePlugin(t, root, "svc", manifest, nil)

	set := StartServices(root, []string{"TY_API_URL=http://127.0.0.1:8080"}, nil)
	t.Cleanup(set.Stop)

	var got string
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			got = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != "http://127.0.0.1:8080" {
		t.Errorf("TY_API_URL in service = %q, want the injected value", got)
	}
}
