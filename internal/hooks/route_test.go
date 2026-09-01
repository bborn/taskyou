package hooks

import (
	"context"
	"os"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
)

func routeRunner(t *testing.T, root string) *Runner {
	t.Helper()
	return newRunner("", root, log.NewWithOptions(os.Stderr, log.Options{Level: log.FatalLevel}))
}

func TestParseRouteOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want RouteDecision
	}{
		{
			name: "config dir",
			out:  "CLAUDE_CONFIG_DIR=/home/me/.claude-work\n",
			want: RouteDecision{ClaudeConfigDir: "/home/me/.claude-work"},
		},
		{
			name: "quoted value",
			out:  "CLAUDE_CONFIG_DIR=\"/home/me/my claude\"\n",
			want: RouteDecision{ClaudeConfigDir: "/home/me/my claude"},
		},
		{
			name: "hold with reason",
			out:  "HOLD=1\nREASON=all profiles above 90%\n",
			want: RouteDecision{Hold: true, Reason: "all profiles above 90%"},
		},
		{
			name: "defer is an alias for hold",
			out:  "DEFER=true\n",
			want: RouteDecision{Hold: true},
		},
		{
			name: "hold=0 is not a hold",
			out:  "HOLD=0\nCLAUDE_CONFIG_DIR=/a\n",
			want: RouteDecision{ClaudeConfigDir: "/a"},
		},
		{
			// A router's stdout is decision-only, but scripts still leak the odd
			// line. Anything unrecognized must be inert rather than fatal.
			name: "noise, comments and blank lines are ignored",
			out:  "\n# picking a profile\nchecking usage...\nCLAUDE_CONFIG_DIR=/a\nUNKNOWN_KEY=x\n",
			want: RouteDecision{ClaudeConfigDir: "/a"},
		},
		{
			name: "value containing = is kept whole",
			out:  "REASON=used=93%\nHOLD=yes\n",
			want: RouteDecision{Hold: true, Reason: "used=93%"},
		},
		{
			name: "empty output is no opinion",
			out:  "",
			want: RouteDecision{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRouteOutput(tc.out)
			if got != tc.want {
				t.Errorf("ParseRouteOutput(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

func TestRouteDecisionEmpty(t *testing.T) {
	if !(RouteDecision{}).Empty() {
		t.Error("zero decision should be empty")
	}
	if (RouteDecision{ClaudeConfigDir: "/a"}).Empty() {
		t.Error("decision with a config dir is not empty")
	}
	if (RouteDecision{Hold: true}).Empty() {
		t.Error("hold decision is not empty")
	}
	// A reason on its own carries no instruction, so it must not count as an
	// answer — otherwise a script that only logged would silently shadow the
	// next router in line.
	if !(RouteDecision{Reason: "just saying"}).Empty() {
		t.Error("reason-only decision should be empty")
	}
}

func TestRoute_AppliesDecisionAndInjectsEnv(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "router",
		"name: router\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho \"CLAUDE_CONFIG_DIR=/dirs/$TASK_PROJECT-$TASK_ID-$TASK_EXECUTOR\"\n"})

	r := routeRunner(t, root)
	if !r.HandlesRoute() {
		t.Fatal("HandlesRoute() = false, want true")
	}

	task := &db.Task{ID: 7, Title: "t", Project: "proj", Executor: db.ExecutorClaude}
	got := r.Route(context.Background(), task)
	if got.ClaudeConfigDir != "/dirs/proj-7-claude" {
		t.Errorf("ClaudeConfigDir = %q", got.ClaudeConfigDir)
	}
	if got.Plugin != "router" {
		t.Errorf("Plugin = %q, want router", got.Plugin)
	}
}

func TestRoute_FirstNonEmptyDecisionWinsInNameOrder(t *testing.T) {
	root := t.TempDir()
	// "a-quiet" sorts first but abstains; "b-router" must then be consulted.
	writePlugin(t, root, "a-quiet",
		"name: a-quiet\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\nexit 0\n"})
	writePlugin(t, root, "b-router",
		"name: b-router\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/from-b\n"})
	writePlugin(t, root, "c-router",
		"name: c-router\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/from-c\n"})

	r := routeRunner(t, root)
	got := r.Route(context.Background(), &db.Task{ID: 1, Executor: db.ExecutorClaude})
	if got.ClaudeConfigDir != "/from-b" {
		t.Errorf("ClaudeConfigDir = %q, want /from-b (first answering plugin by name)", got.ClaudeConfigDir)
	}
}

func TestRoute_FailingScriptIsSkipped(t *testing.T) {
	root := t.TempDir()
	// A router that prints a decision *and* exits non-zero must not be trusted:
	// a half-finished script's last echo is not a decision.
	writePlugin(t, root, "a-broken",
		"name: a-broken\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/bad\nexit 3\n"})
	writePlugin(t, root, "b-good",
		"name: b-good\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/good\n"})

	r := routeRunner(t, root)
	got := r.Route(context.Background(), &db.Task{ID: 1, Executor: db.ExecutorClaude})
	if got.ClaudeConfigDir != "/good" {
		t.Errorf("ClaudeConfigDir = %q, want /good", got.ClaudeConfigDir)
	}
}

func TestRoute_StderrIsNotParsedAsDecision(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "router",
		"name: router\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho 'CLAUDE_CONFIG_DIR=/from-stderr' >&2\necho CLAUDE_CONFIG_DIR=/from-stdout\n"})

	r := routeRunner(t, root)
	got := r.Route(context.Background(), &db.Task{ID: 1, Executor: db.ExecutorClaude})
	if got.ClaudeConfigDir != "/from-stdout" {
		t.Errorf("ClaudeConfigDir = %q, want /from-stdout", got.ClaudeConfigDir)
	}
}

func TestRoute_NoRoutePluginsIsNoOpinion(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "notifier",
		"name: notifier\nhooks:\n  task.done: done.sh\n",
		map[string]string{"done.sh": "#!/bin/sh\n"})

	r := routeRunner(t, root)
	if r.HandlesRoute() {
		t.Error("HandlesRoute() = true with no task.route hook")
	}
	if got := r.Route(context.Background(), &db.Task{ID: 1}); !got.Empty() {
		t.Errorf("Route = %+v, want empty", got)
	}
}

func TestRoute_NilTaskIsSafe(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "router",
		"name: router\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/x\n"})

	if got := routeRunner(t, root).Route(context.Background(), nil); !got.Empty() {
		t.Errorf("Route(nil) = %+v, want empty", got)
	}
}

func TestRoute_CancelledContextYieldsNoDecision(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "router",
		"name: router\nhooks:\n  task.route: route.sh\n",
		map[string]string{"route.sh": "#!/bin/sh\necho CLAUDE_CONFIG_DIR=/x\n"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := routeRunner(t, root).Route(ctx, &db.Task{ID: 1}); !got.Empty() {
		t.Errorf("Route with cancelled ctx = %+v, want empty (spawn as configured)", got)
	}
}
