package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// PR status is read with ONE GraphQL query per repository that names every
// branch we care about as an aliased pullRequests(headRefName:) field. That
// replaces the old `gh pr list` + per-branch `gh pr view` + `gh api rate_limit`
// trio: a single call returns open, merged and closed PRs alike (so there is no
// "absent from the open list, go fetch it" reconcile step), carries the commit's
// CI rollup as one enum instead of expanding every check run, and reports its own
// rate-limit budget. Measured cost is 1 point for a handful of branches.

// maxBranchesPerQuery bounds one GraphQL document. Each alias asks for a few
// nodes, so 50 branches stays far under GitHub's node and cost limits.
const maxBranchesPerQuery = 50

// ghQueryTimeout caps one GraphQL round trip.
const ghQueryTimeout = 20 * time.Second

// ErrGHUnavailable means gh is missing or cannot authenticate. Callers treat it
// like any other failed lookup: keep the last known state and back off.
var ErrGHUnavailable = errors.New("gh CLI unavailable")

// RateLimitError reports that GitHub refused the query for rate limiting.
// ResetAt is zero when GitHub didn't say when the budget returns.
type RateLimitError struct {
	ResetAt time.Time
	Detail  string
}

func (e *RateLimitError) Error() string {
	if e.ResetAt.IsZero() {
		return "github rate limit: " + e.Detail
	}
	return fmt.Sprintf("github rate limit until %s: %s", e.ResetAt.Format(time.RFC3339), e.Detail)
}

// BranchPRs is the answer to one batched lookup.
type BranchPRs struct {
	// PRs maps branch name to its PR. A branch that was asked about and is
	// absent here definitively has no PR — the query succeeded.
	PRs map[string]*PRInfo
	// RateRemaining is GraphQL budget left after the query, -1 if unknown.
	RateRemaining int
	RateResetAt   time.Time
}

// runGH executes gh. Tests replace it.
var runGH = func(ctx context.Context, dir string, args ...string) (stdout, stderr []byte, err error) {
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		return nil, nil, ErrGHUnavailable
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// originURL reads a checkout's origin remote. Tests replace it.
var originURL = func(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("read origin remote of %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var repoRefCache = &sync.Map{} // repoDir -> RepoRef

// RepoForDir resolves which GitHub repository a checkout's origin points at,
// locally and without an API call. Successful answers are cached per directory.
func RepoForDir(dir string) (RepoRef, error) {
	if dir == "" {
		return RepoRef{}, errors.New("no repository directory")
	}
	if ref, ok := repoRefCache.Load(dir); ok {
		return ref.(RepoRef), nil
	}
	url, err := originURL(dir)
	if err != nil {
		return RepoRef{}, err
	}
	ref, err := ParseRepoRef(url)
	if err != nil {
		return RepoRef{}, fmt.Errorf("origin of %s is not a GitHub repo: %w", dir, err)
	}
	repoRefCache.Store(dir, ref)
	return ref, nil
}

// LookupPR returns the PR for one branch. (nil, nil) means the branch has no PR;
// a non-nil error means GitHub could not be asked and the caller must not treat
// the task as PR-less.
func LookupPR(ctx context.Context, repoDir, branch string) (*PRInfo, error) {
	if branch == "" {
		return nil, nil
	}
	res, err := FetchPRsForBranches(ctx, repoDir, []string{branch})
	if err != nil {
		return nil, err
	}
	return res.PRs[branch], nil
}

// FetchPRsForBranches looks up the newest PR for each branch in the repository
// that repoDir's origin points at, in as few GraphQL calls as the batch size
// allows. Any failed call fails the whole lookup: a partial answer would make
// the missing branches look PR-less.
func FetchPRsForBranches(ctx context.Context, repoDir string, branches []string) (*BranchPRs, error) {
	repo, err := RepoForDir(repoDir)
	if err != nil {
		return nil, err
	}
	branches = uniqueBranches(branches)
	result := &BranchPRs{PRs: make(map[string]*PRInfo), RateRemaining: -1}
	for start := 0; start < len(branches); start += maxBranchesPerQuery {
		end := min(start+maxBranchesPerQuery, len(branches))
		if err := fetchBranchChunk(ctx, repoDir, repo, branches[start:end], result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func uniqueBranches(branches []string) []string {
	seen := make(map[string]bool, len(branches))
	out := make([]string, 0, len(branches))
	for _, b := range branches {
		b = strings.TrimSpace(b)
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

const prFragment = `fragment PRFields on PullRequest {
  number url state isDraft title mergeable mergeStateStatus reviewDecision
  additions deletions updatedAt headRefName
  headRepositoryOwner { login }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

// buildBranchQuery builds the aliased query. Branch names travel as GraphQL
// variables, never spliced into the document, so no branch name can alter it.
func buildBranchQuery(n int) string {
	var vars, fields strings.Builder
	vars.WriteString("$owner: String!, $name: String!")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&vars, ", $b%d: String!", i)
		// first: 5 leaves room to skip same-named branches from forks.
		fmt.Fprintf(&fields, "    b%d: pullRequests(headRefName: $b%d, first: 5, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { ...PRFields } }\n", i, i)
	}
	return fmt.Sprintf("query(%s) {\n  rateLimit { remaining resetAt }\n  repository(owner: $owner, name: $name) {\n%s  }\n}\n%s",
		vars.String(), fields.String(), prFragment)
}

func fetchBranchChunk(ctx context.Context, repoDir string, repo RepoRef, branches []string, into *BranchPRs) error {
	args := []string{"api", "graphql"}
	if !strings.EqualFold(repo.Host, DefaultHost) {
		args = append(args, "--hostname", repo.Host)
	}
	// -f (not -F): -F would coerce a branch named "123" or "true" into a number or bool.
	args = append(args, "-f", "query="+buildBranchQuery(len(branches)), "-f", "owner="+repo.Owner, "-f", "name="+repo.Name)
	for i, b := range branches {
		args = append(args, "-f", fmt.Sprintf("b%d=%s", i, b))
	}

	ctx, cancel := context.WithTimeout(ctx, ghQueryTimeout)
	defer cancel()
	stdout, stderr, runErr := runGH(ctx, repoDir, args...)

	// gh exits non-zero when the response carries GraphQL errors but still prints
	// the body, so parse stdout before deciding what the failure was.
	resp, parseErr := parseBranchResponse(stdout)
	if runErr != nil || parseErr != nil {
		return classifyGHFailure(ctx, runErr, stderr, resp, parseErr)
	}
	if resp.Data.Repository == nil {
		return fmt.Errorf("repository %s not found or not accessible", repo.Slug())
	}

	into.RateRemaining = resp.Data.RateLimit.Remaining
	into.RateResetAt = resp.Data.RateLimit.ResetAt
	for i, b := range branches {
		conn := resp.Data.Repository[fmt.Sprintf("b%d", i)]
		if info := pickBranchPR(conn.Nodes, repo.Owner); info != nil {
			into.PRs[b] = info
		}
	}
	return nil
}

type gqlPR struct {
	Number              int    `json:"number"`
	URL                 string `json:"url"`
	State               string `json:"state"`
	IsDraft             bool   `json:"isDraft"`
	Title               string `json:"title"`
	Mergeable           string `json:"mergeable"`
	MergeStateStatus    string `json:"mergeStateStatus"`
	ReviewDecision      string `json:"reviewDecision"`
	Additions           int    `json:"additions"`
	Deletions           int    `json:"deletions"`
	UpdatedAt           string `json:"updatedAt"`
	HeadRefName         string `json:"headRefName"`
	HeadRepositoryOwner *struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type gqlBranchResponse struct {
	Data struct {
		RateLimit struct {
			Remaining int       `json:"remaining"`
			ResetAt   time.Time `json:"resetAt"`
		} `json:"rateLimit"`
		Repository map[string]struct {
			Nodes []gqlPR `json:"nodes"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

func parseBranchResponse(stdout []byte) (*gqlBranchResponse, error) {
	if len(bytes.TrimSpace(stdout)) == 0 {
		return nil, errors.New("empty response from gh api graphql")
	}
	var resp gqlBranchResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return nil, fmt.Errorf("unparseable response from gh api graphql: %w", err)
	}
	if len(resp.Errors) > 0 {
		return &resp, fmt.Errorf("graphql error: %s", resp.Errors[0].Message)
	}
	return &resp, nil
}

// classifyGHFailure turns a failed gh call into ErrGHUnavailable, a
// *RateLimitError, or a plain error carrying gh's own words.
func classifyGHFailure(ctx context.Context, runErr error, stderr []byte, resp *gqlBranchResponse, parseErr error) error {
	if errors.Is(runErr, ErrGHUnavailable) {
		return runErr
	}
	if ctx.Err() != nil {
		return fmt.Errorf("gh api graphql timed out after %s", ghQueryTimeout)
	}
	detail := strings.TrimSpace(string(stderr))
	if resp != nil {
		for _, e := range resp.Errors {
			if strings.EqualFold(e.Type, "RATE_LIMITED") {
				return &RateLimitError{ResetAt: resp.Data.RateLimit.ResetAt, Detail: e.Message}
			}
		}
		if detail == "" && len(resp.Errors) > 0 {
			detail = resp.Errors[0].Message
		}
	}
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "submitted too quickly"):
		return &RateLimitError{Detail: detail}
	case strings.Contains(lower, "not logged in") || strings.Contains(lower, "no oauth token") ||
		strings.Contains(lower, "gh auth login") || strings.Contains(lower, "bad credentials"):
		return fmt.Errorf("%w: %s", ErrGHUnavailable, detail)
	}
	if detail == "" && parseErr != nil {
		detail = parseErr.Error()
	}
	if detail == "" && runErr != nil {
		detail = runErr.Error()
	}
	return fmt.Errorf("gh api graphql failed: %s", detail)
}

// pickBranchPR returns the newest PR whose head lives in the repository itself.
// A fork can open a PR from a branch of the same name; that PR isn't ours.
func pickBranchPR(nodes []gqlPR, owner string) *PRInfo {
	for i := range nodes {
		n := &nodes[i]
		if n.HeadRepositoryOwner == nil || !strings.EqualFold(n.HeadRepositoryOwner.Login, owner) {
			continue
		}
		return prInfoFromGraphQL(n)
	}
	return nil
}

func prInfoFromGraphQL(n *gqlPR) *PRInfo {
	info := &PRInfo{
		Number:           n.Number,
		URL:              n.URL,
		Title:            n.Title,
		IsDraft:          n.IsDraft,
		State:            normalizePRState(n.State, n.IsDraft),
		Mergeable:        strings.ToUpper(n.Mergeable),
		MergeStateStatus: strings.ToUpper(n.MergeStateStatus),
		ReviewDecision:   strings.ToUpper(n.ReviewDecision),
		Additions:        n.Additions,
		Deletions:        n.Deletions,
	}
	if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
		info.CheckState = rollupCheckState(n.Commits.Nodes[0].Commit.StatusCheckRollup.State)
	}
	if t, err := time.Parse(time.RFC3339, n.UpdatedAt); err == nil {
		info.UpdatedAt = t
	}
	return info
}

func normalizePRState(state string, isDraft bool) PRState {
	switch strings.ToUpper(state) {
	case "MERGED":
		return PRStateMerged
	case "CLOSED":
		return PRStateClosed
	default:
		if isDraft {
			return PRStateDraft
		}
		return PRStateOpen
	}
}

// rollupCheckState maps the commit's StatusState rollup to our CheckState.
func rollupCheckState(state string) CheckState {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return CheckStatePassing
	case "FAILURE", "ERROR":
		return CheckStateFailing
	case "PENDING", "EXPECTED":
		return CheckStatePending
	default:
		return CheckStateNone
	}
}
