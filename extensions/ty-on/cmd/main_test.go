package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/extensions/ty-on/internal/placement"
)

// noInventory points the resolver at a path that does not exist, so these tests
// exercise the stdin/stdout contract without depending on a real fleet.
func noInventory(t *testing.T) {
	t.Helper()
	t.Setenv("ON_HOSTS", filepath.Join(t.TempDir(), "absent.yaml"))
	t.Setenv("PATH", t.TempDir())
}

func TestResolveReadsTheRequestContract(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		// wantReason is a substring the reason must contain.
		wantReason string
	}{
		{
			name:       "a well-formed request is understood",
			stdin:      `{"event":"task.placement","task":{"id":5225,"title":"Some task","project":"taskyou","repo_path":"/Users/bruno/Projects/workflow","executor":"claude"}}`,
			wantReason: "no host inventory at",
		},
		{
			name:       "malformed JSON falls back to local",
			stdin:      `{"event":"task.placement",`,
			wantReason: "placement request is not valid JSON",
		},
		{
			name:       "a JSON scalar falls back to local",
			stdin:      `"nope"`,
			wantReason: "placement request is not valid JSON",
		},
		{
			name:       "empty stdin falls back to local",
			stdin:      "",
			wantReason: "empty placement request",
		},
		{
			name:       "a request with no task falls back to local",
			stdin:      `{"event":"task.placement"}`,
			wantReason: "task has no project",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			noInventory(t)

			got := resolve(context.Background(), strings.NewReader(tc.stdin))

			if got.Target != "" {
				t.Errorf("target = %q, want a local placement", got.Target)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// Whatever happens, the response must be one JSON object carrying all three
// fields — core parses it unconditionally.
func TestResolveAlwaysEncodesTheFullResponse(t *testing.T) {
	noInventory(t)

	out, err := json.Marshal(resolve(context.Background(), strings.NewReader("garbage")))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatalf("response is not a JSON object: %v (%s)", err, out)
	}
	for _, key := range []string{"target", "workdir", "reason"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("response is missing %q: %s", key, out)
		}
	}
	if fields["reason"] == "" {
		t.Errorf("reason is empty: %s", out)
	}
}

func TestTimeout(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", placement.DefaultTimeout},
		{"750ms", 750 * time.Millisecond},
		{"10s", 10 * time.Second},
		{"nonsense", placement.DefaultTimeout},
		{"0s", placement.DefaultTimeout},
		{"-5s", placement.DefaultTimeout},
	}

	for _, tc := range tests {
		t.Setenv("TY_ON_TIMEOUT", tc.raw)
		if got := timeout(); got != tc.want {
			t.Errorf("TY_ON_TIMEOUT=%q: timeout() = %s, want %s", tc.raw, got, tc.want)
		}
	}
}
