package github

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGH stands in for gh and git for one test.
func fakeGH(t *testing.T, origin string, run func(args []string) (stdout, stderr string, err error)) {
	t.Helper()
	prevRun, prevOrigin := runGH, originURL
	runGH = func(ctx context.Context, dir string, args ...string) ([]byte, []byte, error) {
		out, errOut, err := run(args)
		return []byte(out), []byte(errOut), err
	}
	originURL = func(dir string) (string, error) { return origin, nil }
	repoRefCache = &sync.Map{}
	t.Cleanup(func() {
		runGH, originURL = prevRun, prevOrigin
		repoRefCache = &sync.Map{}
	})
}

// argValue returns the value of a `-f key=value` pair.
func argValue(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" && strings.HasPrefix(args[i+1], key+"=") {
			return strings.TrimPrefix(args[i+1], key+"=")
		}
	}
	return ""
}

const branchResponse = `{"data":{
  "rateLimit":{"remaining":4406,"resetAt":"2026-09-15T21:31:04Z"},
  "repository":{
    "b0":{"nodes":[]},
    "b1":{"nodes":[
      {"number":9,"url":"https://github.com/fork/taskyou/pull/9","state":"OPEN","isDraft":false,"title":"fork pr",
       "headRefName":"task/1-fix","headRepositoryOwner":{"login":"someone-else"},"commits":{"nodes":[]}},
      {"number":675,"url":"https://github.com/bborn/taskyou/pull/675","state":"OPEN","isDraft":false,"title":"real pr",
       "mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","reviewDecision":"REVIEW_REQUIRED",
       "additions":265,"deletions":5,"updatedAt":"2026-09-15T11:50:15Z","headRefName":"task/1-fix",
       "headRepositoryOwner":{"login":"BBorn"},
       "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"PENDING"}}}]}}
    ]},
    "b2":{"nodes":[
      {"number":600,"url":"https://github.com/bborn/taskyou/pull/600","state":"MERGED","isDraft":false,"title":"merged",
       "headRefName":"task/2-done","headRepositoryOwner":{"login":"bborn"},
       "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}
    ]}
  }}}`

func TestFetchPRsForBranches_OneQueryCoversOpenMergedAndMissing(t *testing.T) {
	calls := 0
	fakeGH(t, "git@github.com:bborn/taskyou.git", func(args []string) (string, string, error) {
		calls++
		if args[0] != "api" || args[1] != "graphql" {
			t.Fatalf("expected gh api graphql, got %v", args)
		}
		// Branches are sorted, so aliases are stable: b0 no-pr, b1 fix, b2 done.
		if got := argValue(args, "b0"); got != "task/0-no-pr" {
			t.Errorf("b0 = %q", got)
		}
		if argValue(args, "owner") != "bborn" || argValue(args, "name") != "taskyou" {
			t.Errorf("owner/name not passed as variables: %v", args)
		}
		return branchResponse, "", nil
	})

	res, err := FetchPRsForBranches(context.Background(), "/repo", []string{"task/2-done", "task/1-fix", "task/0-no-pr", "task/1-fix"})
	if err != nil {
		t.Fatalf("FetchPRsForBranches: %v", err)
	}
	if calls != 1 {
		t.Fatalf("gh calls = %d, want 1", calls)
	}

	open := res.PRs["task/1-fix"]
	if open == nil || open.Number != 675 {
		t.Fatalf("task/1-fix = %+v, want our PR #675 (not the fork's #9)", open)
	}
	if open.State != PRStateOpen || open.CheckState != CheckStatePending ||
		open.MergeStateStatus != "BLOCKED" || open.ReviewDecision != "REVIEW_REQUIRED" ||
		open.Additions != 265 || open.UpdatedAt.IsZero() {
		t.Errorf("open PR fields not carried: %+v", open)
	}

	merged := res.PRs["task/2-done"]
	if merged == nil || merged.State != PRStateMerged || merged.CheckState != CheckStateNone {
		t.Errorf("task/2-done = %+v, want merged with no checks", merged)
	}
	if _, ok := res.PRs["task/0-no-pr"]; ok {
		t.Error("branch without a PR must be absent")
	}
	if res.RateRemaining != 4406 {
		t.Errorf("RateRemaining = %d", res.RateRemaining)
	}
}

func TestFetchPRsForBranches_ChunksLargeBatches(t *testing.T) {
	calls := 0
	fakeGH(t, "https://github.com/bborn/taskyou", func(args []string) (string, string, error) {
		calls++
		return `{"data":{"rateLimit":{"remaining":5000},"repository":{}}}`, "", nil
	})
	branches := make([]string, maxBranchesPerQuery+1)
	for i := range branches {
		branches[i] = "task/" + strings.Repeat("x", i+1)
	}
	if _, err := FetchPRsForBranches(context.Background(), "/repo", branches); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestFetchPRsForBranches_EnterpriseHostPassesHostname(t *testing.T) {
	fakeGH(t, "git@github.acme.com:team/app.git", func(args []string) (string, string, error) {
		if !strings.Contains(strings.Join(args, " "), "--hostname github.acme.com") {
			t.Errorf("missing --hostname: %v", args)
		}
		return `{"data":{"repository":{}}}`, "", nil
	})
	if _, err := FetchPRsForBranches(context.Background(), "/repo", []string{"a"}); err != nil {
		t.Fatal(err)
	}
}

// gh's --hostname validator rejects any value containing ':'. ParseRepoRef
// preserves a ":port" in RepoRef.Host (which is correct for git via CloneURL),
// but feeding the same value to `gh api --hostname` can never succeed. The
// guard must short-circuit and surface a distinguishable sentinel *before* gh
// is spawned, for every URL form that carries a port.
func TestFetchPRsForBranches_PortBearingHostFailsWithoutCallingGH(t *testing.T) {
	cases := []struct {
		name         string
		origin       string
		wantHostPort string
	}{
		{name: "https with port", origin: "https://gh.acme.test:8443/o/r", wantHostPort: "gh.acme.test:8443"},
		{name: "https with port and .git", origin: "https://gh.acme.test:8443/o/r.git", wantHostPort: "gh.acme.test:8443"},
		{name: "ssh url with port", origin: "ssh://git@gh.acme.test:2222/o/r.git", wantHostPort: "gh.acme.test:2222"},
		{name: "github.com with port", origin: "https://github.com:8443/o/r", wantHostPort: "github.com:8443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			fakeGH(t, tc.origin, func(args []string) (string, string, error) {
				calls++
				return "", "", nil
			})
			_, err := FetchPRsForBranches(context.Background(), "/repo", []string{"main"})
			if err == nil {
				t.Fatal("expected an error for a port-bearing host, got nil")
			}
			if !errors.Is(err, ErrUnsupportedHostPort) {
				t.Fatalf("error must wrap ErrUnsupportedHostPort, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantHostPort) {
				t.Fatalf("error should name the offending host:port %q, got %q", tc.wantHostPort, err.Error())
			}
			if calls != 0 {
				t.Fatalf("gh must not be spawned for a port-bearing host, gh calls = %d", calls)
			}
		})
	}
}

// SCP-style git remotes use ':' as the user/repo separator, not a port. The
// guard must not misfire on them.
func TestFetchPRsForBranches_SCPRemoteWithoutPortStillCallsGH(t *testing.T) {
	called := false
	fakeGH(t, "git@github.acme.com:team/app.git", func(args []string) (string, string, error) {
		called = true
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--hostname" {
				if strings.ContainsRune(args[i+1], ':') {
					t.Errorf("SCP host must not carry a port into --hostname: %q", args[i+1])
				}
			}
		}
		return `{"data":{"repository":{}}}`, "", nil
	})
	if _, err := FetchPRsForBranches(context.Background(), "/repo", []string{"a"}); err != nil {
		t.Fatalf("SCP remote without port must still work: %v", err)
	}
	if !called {
		t.Fatal("gh must be called for a port-less SCP enterprise host")
	}
}

// Failures must come back as errors, never as "no PR" — that confusion is what
// made badges vanish and triggered a fetch per task.
func TestFetchPRsForBranches_FailuresAreErrors(t *testing.T) {
	exitErr := errors.New("exit status 1")
	cases := []struct {
		name      string
		stdout    string
		stderr    string
		err       error
		rateLimit bool
		unavail   bool
	}{
		{name: "graphql rate limited", stdout: `{"data":{"rateLimit":{"remaining":0,"resetAt":"2030-01-01T00:00:00Z"}},"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, err: exitErr, rateLimit: true},
		{name: "secondary rate limit", stderr: "HTTP 403: You have exceeded a secondary rate limit", err: exitErr, rateLimit: true},
		{name: "not logged in", stderr: "To get started with GitHub CLI, please run:  gh auth login", err: exitErr, unavail: true},
		{name: "gh missing", err: ErrGHUnavailable, unavail: true},
		{name: "network", stderr: "dial tcp: lookup api.github.com: no such host", err: exitErr},
		{name: "garbage output", stdout: "<html>", err: nil},
		{name: "repo not accessible", stdout: `{"data":{"repository":null}}`, err: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeGH(t, "git@github.com:bborn/taskyou.git", func([]string) (string, string, error) {
				return tc.stdout, tc.stderr, tc.err
			})
			res, err := FetchPRsForBranches(context.Background(), "/repo", []string{"a"})
			if err == nil {
				t.Fatalf("want error, got %+v", res)
			}
			var rl *RateLimitError
			if got := errors.As(err, &rl); got != tc.rateLimit {
				t.Errorf("rate limit = %v, want %v (%v)", got, tc.rateLimit, err)
			}
			if tc.name == "graphql rate limited" && !rl.ResetAt.Equal(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("ResetAt = %v", rl.ResetAt)
			}
			if got := errors.Is(err, ErrGHUnavailable); got != tc.unavail {
				t.Errorf("unavailable = %v, want %v (%v)", got, tc.unavail, err)
			}
		})
	}
}

func TestLookupPR_NoPRIsNilNil(t *testing.T) {
	fakeGH(t, "git@github.com:bborn/taskyou.git", func([]string) (string, string, error) {
		return `{"data":{"repository":{"b0":{"nodes":[]}}}}`, "", nil
	})
	info, err := LookupPR(context.Background(), "/repo", "task/none")
	if info != nil || err != nil {
		t.Fatalf("LookupPR = %v, %v; want nil, nil", info, err)
	}
}

func TestBuildBranchQuery_BranchesAreVariables(t *testing.T) {
	q := buildBranchQuery(2)
	for _, want := range []string{"$b0: String!", "$b1: String!", "b1: pullRequests(headRefName: $b1", "rateLimit", "statusCheckRollup { state }"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

func TestRollupCheckState(t *testing.T) {
	cases := map[string]CheckState{
		"SUCCESS": CheckStatePassing, "FAILURE": CheckStateFailing, "ERROR": CheckStateFailing,
		"PENDING": CheckStatePending, "EXPECTED": CheckStatePending, "": CheckStateNone,
	}
	for in, want := range cases {
		if got := rollupCheckState(in); got != want {
			t.Errorf("rollupCheckState(%q) = %q, want %q", in, got, want)
		}
	}
}
