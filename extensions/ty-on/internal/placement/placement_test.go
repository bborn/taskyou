package placement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fleet is an inventory where offerlab has exactly one host and taskyou has two,
// so the one-candidate and several-candidate rules can both be exercised.
const fleet = `
repos:
  taskyou: git@github.com:bborn/taskyou.git

hosts:
  ol-agents:
    ssh: ol-agents
    workdir: ~/projects
    capabilities: [agent, ruby]
    repos:
      offerlab: ~/projects/engineering

  mona:
    ssh: mona
    workdir: ~/Projects
    capabilities: [agent, docker]
    repos:
      taskyou: ~/Projects/taskyou

  rex:
    ssh: rex
    workdir: /root
    capabilities: [agent]
    repos:
      taskyou: /root/taskyou
`

// Free memory as `on ls` would report it, in the kilobytes HostStat carries.
const (
	mem11585M = 11585 * 1024
	mem2048M  = 2048 * 1024
)

// writeInventory writes body to a temp file and points ON_HOSTS at it.
func writeInventory(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write inventory: %v", err)
	}
	t.Setenv("ON_HOSTS", path)
	return path
}

// stubProber stands in for `on ls` so no test needs the real CLI on PATH.
type stubProber struct {
	stats map[string]HostStat
	err   error
	// blocks makes Probe wait for the resolver's deadline instead of answering.
	blocks bool
	calls  int
}

func (p *stubProber) Probe(ctx context.Context) (map[string]HostStat, error) {
	p.calls++
	if p.blocks {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.stats, p.err
}

// unusedProber fails the test if the resolver reaches for a probe it should not
// need — the zero- and one-candidate rules must answer without measuring anything.
type unusedProber struct{ t *testing.T }

func (p unusedProber) Probe(context.Context) (map[string]HostStat, error) {
	p.t.Helper()
	p.t.Error("probed the fleet for a placement that needs no comparison")
	return nil, errors.New("should not be called")
}

func reachable(kb int64) HostStat { return HostStat{Reachable: true, FreeKB: kb} }

func unreachable() HostStat { return HostStat{} }

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		// inventory is written to a temp file and exposed via ON_HOSTS. Empty
		// means "point ON_HOSTS at a path that does not exist".
		inventory string
		project   string
		event     string
		// prober stands in for `on ls`. Nil means the placement must not probe.
		prober      Prober
		wantTarget  string
		wantWorkdir string
		// wantReason is a substring the reason must contain.
		wantReason string
	}{
		{
			name:       "unknown project falls back to local",
			inventory:  fleet,
			project:    "influencekit",
			wantTarget: "",
			wantReason: "serves influencekit (3 hosts in inventory)",
		},
		{
			name:        "single host serving the project wins outright",
			inventory:   fleet,
			project:     "offerlab",
			wantTarget:  "ol-agents",
			wantWorkdir: "~/projects/engineering",
			wantReason:  "only host serving offerlab",
		},
		{
			name:      "several hosts are ranked by free memory",
			inventory: fleet,
			project:   "taskyou",
			prober: &stubProber{stats: map[string]HostStat{
				"mona": reachable(mem11585M),
				"rex":  reachable(mem2048M),
				// Not a candidate: roomiest host, but no taskyou checkout.
				"ol-agents": reachable(26565 * 1024),
			}},
			wantTarget:  "mona",
			wantWorkdir: "~/Projects/taskyou",
			wantReason:  "most free memory of 2 hosts serving taskyou (mona 11.3G, rex 2.0G)",
		},
		{
			name:      "unreachable candidates are skipped",
			inventory: fleet,
			project:   "taskyou",
			prober: &stubProber{stats: map[string]HostStat{
				"mona": unreachable(),
				"rex":  reachable(mem2048M),
			}},
			wantTarget:  "rex",
			wantWorkdir: "/root/taskyou",
			wantReason:  "only reachable host of 2 serving taskyou (mona, rex)",
		},
		{
			name:      "no reachable candidate falls back to local",
			inventory: fleet,
			project:   "taskyou",
			prober: &stubProber{stats: map[string]HostStat{
				"mona": unreachable(),
				"rex":  unreachable(),
			}},
			wantTarget: "",
			wantReason: "none of them are reachable (mona, rex)",
		},
		{
			name:       "a candidate the probe never mentions is skipped",
			inventory:  fleet,
			project:    "taskyou",
			prober:     &stubProber{stats: map[string]HostStat{"mona": reachable(mem11585M)}},
			wantTarget: "mona",
			// rex was in the inventory but not in the probe: treated as absent.
			wantWorkdir: "~/Projects/taskyou",
			wantReason:  "only reachable host of 2 serving taskyou",
		},
		{
			name:       "missing inventory falls back to local",
			inventory:  "",
			project:    "taskyou",
			wantTarget: "",
			wantReason: "no host inventory at",
		},
		{
			name:       "malformed inventory falls back to local",
			inventory:  "hosts: [this is not: a mapping\n",
			project:    "taskyou",
			wantTarget: "",
			wantReason: "is not valid YAML",
		},
		{
			name:       "empty inventory falls back to local",
			inventory:  "hosts: {}\n",
			project:    "taskyou",
			wantTarget: "",
			wantReason: "inventory lists no hosts",
		},
		{
			name:       "an unusable probe falls back to local",
			inventory:  fleet,
			project:    "taskyou",
			prober:     &stubProber{err: errors.New("the on CLI is not installed")},
			wantTarget: "",
			wantReason: "2 hosts serve taskyou but they could not be compared: the on CLI is not installed",
		},
		{
			name:       "a task without a project falls back to local",
			inventory:  fleet,
			project:    "",
			wantTarget: "",
			wantReason: "task has no project",
		},
		{
			name:       "an unknown event falls back to local",
			inventory:  fleet,
			project:    "taskyou",
			event:      "task.started",
			wantTarget: "",
			wantReason: `unsupported event "task.started"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.inventory == "" {
				t.Setenv("ON_HOSTS", filepath.Join(t.TempDir(), "absent.yaml"))
			} else {
				writeInventory(t, tc.inventory)
			}

			req := request(tc.project)
			if tc.event != "" {
				req.Event = tc.event
			}

			prober := tc.prober
			if prober == nil {
				prober = unusedProber{t}
			}

			got := Resolver{Prober: prober}.Resolve(context.Background(), req)

			if got.Target != tc.wantTarget {
				t.Errorf("target = %q, want %q (reason: %s)", got.Target, tc.wantTarget, got.Reason)
			}
			if got.Workdir != tc.wantWorkdir {
				t.Errorf("workdir = %q, want %q", got.Workdir, tc.wantWorkdir)
			}
			if got.Reason == "" {
				t.Fatal("reason is empty; every placement must explain itself")
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// A slow probe must not hold up the spawn path: we prefer local to a late answer.
func TestResolveGivesUpOnASlowProbe(t *testing.T) {
	writeInventory(t, fleet)
	prober := &stubProber{blocks: true}

	start := time.Now()
	got := Resolver{Prober: prober, Timeout: 50 * time.Millisecond}.
		Resolve(context.Background(), request("taskyou"))
	elapsed := time.Since(start)

	if got.Target != "" {
		t.Errorf("target = %q, want a local placement", got.Target)
	}
	if want := "comparing them took longer than 50ms"; !strings.Contains(got.Reason, want) {
		t.Errorf("reason = %q, want it to contain %q", got.Reason, want)
	}
	if elapsed > time.Second {
		t.Errorf("took %s, want the probe to be abandoned promptly", elapsed)
	}
}

// Ties must not depend on Go's map iteration order.
func TestResolveBreaksMemoryTiesByName(t *testing.T) {
	writeInventory(t, fleet)
	prober := &stubProber{stats: map[string]HostStat{
		"mona": reachable(4096 * 1024),
		"rex":  reachable(4096 * 1024),
	}}

	for i := 0; i < 10; i++ {
		got := Resolver{Prober: prober}.Resolve(context.Background(), request("taskyou"))
		if got.Target != "mona" {
			t.Fatalf("run %d: target = %q, want the alphabetically first of the tied hosts", i, got.Target)
		}
	}
}

// The single-candidate rule must answer from the inventory alone, so a fleet
// without `on` installed still places tasks.
func TestResolveDoesNotProbeForASingleCandidate(t *testing.T) {
	writeInventory(t, fleet)
	prober := &stubProber{stats: map[string]HostStat{"ol-agents": reachable(mem2048M)}}

	got := Resolver{Prober: prober}.Resolve(context.Background(), request("offerlab"))

	if got.Target != "ol-agents" {
		t.Errorf("target = %q, want ol-agents (reason: %s)", got.Target, got.Reason)
	}
	if prober.calls != 0 {
		t.Errorf("probed %d times, want 0", prober.calls)
	}
}

// `on` is an optional dependency. With it absent, the default prober must
// report that plainly and the task must stay local rather than fail.
func TestResolveWhenOnIsNotInstalled(t *testing.T) {
	writeInventory(t, fleet)
	// An empty PATH: exec.LookPath cannot find `on` anywhere.
	t.Setenv("PATH", t.TempDir())

	got := Resolver{}.Resolve(context.Background(), request("taskyou"))

	if got.Target != "" {
		t.Errorf("target = %q, want a local placement", got.Target)
	}
	if want := "the on CLI is not installed"; !strings.Contains(got.Reason, want) {
		t.Errorf("reason = %q, want it to contain %q", got.Reason, want)
	}
}

func TestOnProberReportsAMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := OnProber{}.Probe(context.Background())

	if err == nil {
		t.Fatal("Probe() succeeded, want an error naming the missing CLI")
	}
	if want := "the on CLI is not installed"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err, want)
	}
}

func TestServingIsSortedAndIgnoresBlankCheckouts(t *testing.T) {
	inv, err := LoadInventory(writeInventory(t, `
hosts:
  zeta:
    repos: {taskyou: /srv/taskyou}
  alpha:
    repos: {taskyou: /home/alpha/taskyou}
  blank:
    repos: {taskyou: ""}
  other:
    repos: {offerlab: /srv/offerlab}
`))
	if err != nil {
		t.Fatalf("load inventory: %v", err)
	}

	got := inv.Serving("taskyou")
	want := []string{"alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("serving = [%s], want %v", names(got), want)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("serving[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestInventoryPathPrefersONHOSTS(t *testing.T) {
	t.Setenv("ON_HOSTS", "/tmp/custom-hosts.yaml")
	if got := InventoryPath(); got != "/tmp/custom-hosts.yaml" {
		t.Errorf("InventoryPath() = %q, want the ON_HOSTS value", got)
	}

	t.Setenv("ON_HOSTS", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := InventoryPath(), filepath.Join("/xdg", "on", "hosts.yaml"); got != want {
		t.Errorf("InventoryPath() = %q, want %q", got, want)
	}
}

// The real `on ls` table, verbatim, so the parser is pinned to the actual
// output shape rather than to a tidied-up version of it.
const lsTable = `HOST           SSH                 CORES    AVAIL    TOTAL    LOAD
mona           mona                    4   11585M   15887M    0.02  72% free
ol-agents      ol-agents              16   26565M   31337M    0.12  84% free
down           down                    -        -        -       -  ssh: Could not resolve hostname down: nodename nor servname provided
`

func TestParseOnLS(t *testing.T) {
	stats := ParseOnLS(lsTable)

	if len(stats) != 3 {
		t.Fatalf("parsed %d rows, want 3: %v", len(stats), stats)
	}
	if s := stats["ol-agents"]; !s.Reachable || s.FreeKB != 26565*1024 {
		t.Errorf("ol-agents = %+v, want reachable with 26565M free", s)
	}
	if s := stats["mona"]; !s.Reachable || s.FreeKB != mem11585M {
		t.Errorf("mona = %+v, want reachable with 11585M free", s)
	}
	if s := stats["down"]; s.Reachable {
		t.Errorf("down = %+v, want unreachable", s)
	}
	if _, ok := stats["HOST"]; ok {
		t.Error("the header row was parsed as a host")
	}
}

func TestParseOnLSIgnoresJunk(t *testing.T) {
	if stats := ParseOnLS(""); len(stats) != 0 {
		t.Errorf("empty output parsed as %v, want no hosts", stats)
	}
	if stats := ParseOnLS("on: no inventory found\n"); len(stats) != 0 {
		t.Errorf("short line parsed as %v, want no hosts", stats)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in     string
		want   int64
		wantOK bool
	}{
		{"8777M", 8777 * 1024, true},
		{"512K", 512, true},
		{"2G", 2 * 1024 * 1024, true},
		{"1.5G", 1536 * 1024, true},
		{"1T", 1024 * 1024 * 1024, true},
		{"4096", 4096, true},
		{"-", 0, false},
		{"", 0, false},
		{"ssh:", 0, false},
		{"-1M", 0, false},
	}

	for _, tc := range tests {
		got, ok := parseSize(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("parseSize(%q) = (%d, %t), want (%d, %t)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestHumanKB(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{11585 * 1024, "11.3G"},
		{2048 * 1024, "2.0G"},
		{512 * 1024, "512M"},
		{900, "900K"},
	}

	for _, tc := range tests {
		if got := humanKB(tc.in); got != tc.want {
			t.Errorf("humanKB(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func request(project string) Request {
	return Request{
		Event: Event,
		Task: Task{
			ID:       5225,
			Title:    "Some task",
			Project:  project,
			RepoPath: "/Users/bruno/Projects/workflow",
			Executor: "claude",
		},
	}
}
