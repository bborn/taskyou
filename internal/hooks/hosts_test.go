package hooks

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// hostsRunner builds a Runner over a temp plugins dir with a budget generous
// enough that forking a shell script cannot lose the race under a loaded
// `go test ./...`. Only the timeout test shortens it.
func hostsRunner(t *testing.T, pluginsDir string) *Runner {
	t.Helper()
	return hostsRunnerWithTimeout(t, pluginsDir, 30*time.Second)
}

func hostsRunnerWithTimeout(t *testing.T, pluginsDir string, budget time.Duration) *Runner {
	t.Helper()
	r := newRunner("", pluginsDir, log.NewWithOptions(nil, log.Options{Level: log.FatalLevel}))
	r.hostsTimeoutOverride = budget
	return r
}

// hostsPlugin writes a plugin whose task.hosts handler prints body verbatim.
func hostsPlugin(t *testing.T, root, name, body string) {
	t.Helper()
	writePlugin(t, root, name, "name: "+name+"\nhooks:\n  task.hosts: hosts.sh\n",
		map[string]string{"hosts.sh": "#!/bin/sh\ncat <<'JSON'\n" + body + "\nJSON\n"})
}

// The common case: no placement plugin. Nothing is consulted and no choice is
// offered, which is what every user without a fleet must keep seeing.
func TestListHosts_NoHandlerOffersNothing(t *testing.T) {
	r := hostsRunner(t, t.TempDir())
	if r.HasHostsHandler() {
		t.Fatal("HasHostsHandler() = true with no plugins installed")
	}
	if got := r.ListHosts(context.Background(), placementTask()); len(got) != 0 {
		t.Errorf("ListHosts() = %+v, want none", got)
	}
}

func TestListHosts_ReturnsWhatTheHandlerAnswered(t *testing.T) {
	root := t.TempDir()
	hostsPlugin(t, root, "ty-on",
		`{"hosts":[{"name":"ol-agents","target":"agents.example","workdir":"~/projects/engineering","detail":"agent, ruby"}]}`)

	got := hostsRunner(t, root).ListHosts(context.Background(), placementTask())

	if len(got) != 1 {
		t.Fatalf("ListHosts() = %+v, want one host", got)
	}
	want := Host{Name: "ol-agents", Target: "agents.example", WorkDir: "~/projects/engineering", Detail: "agent, ruby"}
	if got[0] != want {
		t.Errorf("host = %+v, want %+v", got[0], want)
	}
}

// A host identified only one way is still usable: the name is what a person
// reads and the target is what ty records, so each fills in for the other.
func TestListHosts_NameAndTargetFillEachOtherIn(t *testing.T) {
	root := t.TempDir()
	hostsPlugin(t, root, "ty-on",
		`{"hosts":[{"name":"bare"},{"target":"only-a-target"},{"detail":"nothing to place onto"}]}`)

	got := hostsRunner(t, root).ListHosts(context.Background(), placementTask())

	if len(got) != 2 {
		t.Fatalf("ListHosts() = %+v, want the two identifiable hosts", got)
	}
	if got[0].Target != "bare" {
		t.Errorf("target = %q, want the name used as the destination", got[0].Target)
	}
	if got[1].Name != "only-a-target" {
		t.Errorf("name = %q, want the destination used as the label", got[1].Name)
	}
}

// Unlike placement, every handler is asked and the answers add up: two fleets
// installed side by side are two sets of candidates, not a race. A host offered
// twice keeps the first description, so the order is plugin name order.
func TestListHosts_MergesEveryHandler(t *testing.T) {
	root := t.TempDir()
	hostsPlugin(t, root, "a-fleet", `{"hosts":[{"name":"shared","detail":"from a-fleet"},{"name":"only-a"}]}`)
	hostsPlugin(t, root, "b-fleet", `{"hosts":[{"name":"shared","detail":"from b-fleet"},{"name":"only-b"}]}`)

	got := hostsRunner(t, root).ListHosts(context.Background(), placementTask())

	if len(got) != 3 {
		t.Fatalf("ListHosts() = %+v, want shared, only-a and only-b", got)
	}
	if got[0].Name != "shared" || got[0].Detail != "from a-fleet" {
		t.Errorf("first host = %+v, want the first handler's description of shared", got[0])
	}
}

// A handler that cannot answer contributes nothing, and does not stop the one
// after it: a broken plugin must not hide a working fleet.
func TestListHosts_UnusableAnswersAreSkipped(t *testing.T) {
	root := t.TempDir()
	hostsPlugin(t, root, "a-broken", "not json at all")
	writePlugin(t, root, "b-crashes", "name: b-crashes\nhooks:\n  task.hosts: hosts.sh\n",
		map[string]string{"hosts.sh": "#!/bin/sh\nexit 3\n"})
	hostsPlugin(t, root, "c-works", `{"hosts":[{"name":"mona"}]}`)

	got := hostsRunner(t, root).ListHosts(context.Background(), placementTask())

	if len(got) != 1 || got[0].Name != "mona" {
		t.Fatalf("ListHosts() = %+v, want just the host the working handler named", got)
	}
}

// Silence means "no candidates", which is the honest answer for a project no
// host serves — not an error, and not a reason to log loudly.
func TestListHosts_SilenceIsNoCandidates(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.hosts: hosts.sh\n",
		map[string]string{"hosts.sh": "#!/bin/sh\nexit 0\n"})

	if got := hostsRunner(t, root).ListHosts(context.Background(), placementTask()); len(got) != 0 {
		t.Errorf("ListHosts() = %+v, want none", got)
	}
}

// A slow handler is dropped rather than waited on: a human is looking at a form,
// and no picker at all beats a form that hangs.
func TestListHosts_SlowHandlerIsDropped(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.hosts: hosts.sh\n",
		map[string]string{"hosts.sh": "#!/bin/sh\nsleep 5\necho '{\"hosts\":[{\"name\":\"late\"}]}'\n"})

	got := hostsRunnerWithTimeout(t, root, 50*time.Millisecond).
		ListHosts(context.Background(), placementTask())

	if len(got) != 0 {
		t.Errorf("ListHosts() = %+v, want none: a late answer is no answer", got)
	}
}

// The handler is given the same request shape placement gets, so a plugin can
// share one decoder — and it has to know the project to answer at all.
func TestListHosts_HandlerSeesTheRequest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "ty-on", "name: ty-on\nhooks:\n  task.hosts: hosts.sh\n",
		map[string]string{"hosts.sh": `#!/bin/sh
req=$(cat)
case "$req" in
  *'"event":"task.hosts"'*'"project":"taskyou"'*'"executor":"claude"'*)
    echo '{"hosts":[{"name":"understood"}]}' ;;
  *) echo "unexpected request: $req" >&2 ;;
esac
`})

	got := hostsRunner(t, root).ListHosts(context.Background(), placementTask())

	if len(got) != 1 || got[0].Name != "understood" {
		t.Fatalf("ListHosts() = %+v, want the handler to have seen event, project and executor", got)
	}
}
