package main

import (
	"context"
	"encoding/json"
	"os"
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

// placementAnswer runs one request through the dispatcher and asserts it came
// back as a placement, which is what every request that is not a hosts request
// must be — including the ones nobody could parse.
func placementAnswer(t *testing.T, stdin string) placement.Response {
	t.Helper()
	got, ok := answer(context.Background(), strings.NewReader(stdin)).(placement.Response)
	if !ok {
		t.Fatalf("answer(%q) did not return a placement response", stdin)
	}
	return got
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

			got := placementAnswer(t, tc.stdin)

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

	out, err := json.Marshal(placementAnswer(t, "garbage"))
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

// The hosts question is answered with candidates, not with a placement: a form
// is asking what the choices are, and "run locally" is not one of them — ty adds
// that itself.
func TestAnswerHostsRequestListsCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	body := `hosts:
  ol-agents:
    ssh: agents.example
    capabilities: [agent, "executor:claude"]
    repos:
      taskyou: ~/projects/taskyou
  laptop:
    repos:
      other: ~/src/other
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write inventory: %v", err)
	}
	t.Setenv("ON_HOSTS", path)

	got, ok := answer(context.Background(), strings.NewReader(
		`{"event":"task.hosts","task":{"project":"taskyou","executor":"claude"}}`)).(placement.HostsResponse)
	if !ok {
		t.Fatal("a task.hosts request did not come back as a hosts response")
	}
	if len(got.Hosts) != 1 {
		t.Fatalf("hosts = %+v, want only the host serving taskyou", got.Hosts)
	}
	if got.Hosts[0].Name != "ol-agents" || got.Hosts[0].Target != "agents.example" {
		t.Errorf("host = %+v, want ol-agents reachable at its ssh destination", got.Hosts[0])
	}
	if got.Hosts[0].Workdir != "~/projects/taskyou" {
		t.Errorf("workdir = %q, want the project's checkout on that host", got.Hosts[0].Workdir)
	}
}

// An empty list is a normal answer, and it must still be a list: the form asks
// unconditionally, and "no hosts" is how a machine with no fleet looks.
func TestAnswerHostsRequestWithNoInventoryIsEmpty(t *testing.T) {
	noInventory(t)

	got, ok := answer(context.Background(), strings.NewReader(
		`{"event":"task.hosts","task":{"project":"taskyou"}}`)).(placement.HostsResponse)
	if !ok {
		t.Fatal("a task.hosts request did not come back as a hosts response")
	}
	if len(got.Hosts) != 0 {
		t.Errorf("hosts = %+v, want none", got.Hosts)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if string(out) != `{"hosts":[]}` {
		t.Errorf("encoded = %s, want an empty list rather than null", out)
	}
}
