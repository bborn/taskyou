package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SourceKind identifies the type of GitHub object a URL points at.
type SourceKind string

const (
	SourceIssue      SourceKind = "issue"
	SourcePR         SourceKind = "pr"
	SourceDiscussion SourceKind = "discussion"
)

// SourceRef is a parsed reference to a GitHub issue, pull request, or discussion.
type SourceRef struct {
	Owner  string
	Repo   string
	Kind   SourceKind
	Number int
}

// NameWithOwner returns the "owner/repo" slug for the reference.
func (r SourceRef) NameWithOwner() string {
	return r.Owner + "/" + r.Repo
}

// SourceItem holds the details fetched for a GitHub issue/PR/discussion.
type SourceItem struct {
	Ref    SourceRef
	Title  string
	Body   string
	URL    string
	Labels []string
	State  string
	// HeadBranch is the source branch for a pull request (empty for issues/discussions).
	HeadBranch string
}

// sourceURLPattern matches GitHub issue/PR/discussion URLs and captures the
// owner, repo, object type, and number. It accepts optional trailing path or
// query fragments (e.g. "#issuecomment-123").
var sourceURLPattern = regexp.MustCompile(
	`^https?://github\.com/([^/\s]+)/([^/\s]+)/(issues|pull|discussions)/(\d+)`,
)

// ParseSourceURL parses a GitHub issue, pull request, or discussion URL into a
// SourceRef. It returns an error if the URL is not a recognized GitHub URL.
func ParseSourceURL(rawURL string) (SourceRef, error) {
	trimmed := strings.TrimSpace(rawURL)
	m := sourceURLPattern.FindStringSubmatch(trimmed)
	if m == nil {
		return SourceRef{}, fmt.Errorf("not a recognized GitHub issue, pull request, or discussion URL: %q", rawURL)
	}

	number, err := strconv.Atoi(m[4])
	if err != nil {
		return SourceRef{}, fmt.Errorf("invalid number in URL %q: %w", rawURL, err)
	}

	var kind SourceKind
	switch m[3] {
	case "issues":
		kind = SourceIssue
	case "pull":
		kind = SourcePR
	case "discussions":
		kind = SourceDiscussion
	}

	return SourceRef{
		Owner:  m[1],
		Repo:   m[2],
		Kind:   kind,
		Number: number,
	}, nil
}

// ghIssueResponse is the JSON response from `gh issue view`.
type ghIssueResponse struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ghPRSourceResponse is the JSON response from `gh pr view` for source import.
type ghPRSourceResponse struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	State       string `json:"state"`
	HeadRefName string `json:"headRefName"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// FetchSourceItem retrieves the details for a parsed GitHub reference using the
// gh CLI. Issues and pull requests are fetched via `gh issue/pr view`;
// discussions are best-effort via the GraphQL API.
func FetchSourceItem(ref SourceRef) (*SourceItem, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found; install GitHub CLI (https://cli.github.com) to use --from-issue")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch ref.Kind {
	case SourceIssue:
		return fetchIssue(ctx, ref)
	case SourcePR:
		return fetchPR(ctx, ref)
	case SourceDiscussion:
		return fetchDiscussion(ctx, ref)
	default:
		return nil, fmt.Errorf("unsupported source kind: %q", ref.Kind)
	}
}

func fetchIssue(ctx context.Context, ref SourceRef) (*SourceItem, error) {
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", strconv.Itoa(ref.Number),
		"--repo", ref.NameWithOwner(),
		"--json", "title,body,url,state,labels")
	output, err := cmd.Output()
	if err != nil {
		return nil, wrapGHError("fetch issue", err)
	}

	var resp ghIssueResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("parse issue response: %w", err)
	}

	item := &SourceItem{
		Ref:   ref,
		Title: resp.Title,
		Body:  resp.Body,
		URL:   resp.URL,
		State: resp.State,
	}
	for _, l := range resp.Labels {
		if l.Name != "" {
			item.Labels = append(item.Labels, l.Name)
		}
	}
	return item, nil
}

func fetchPR(ctx context.Context, ref SourceRef) (*SourceItem, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(ref.Number),
		"--repo", ref.NameWithOwner(),
		"--json", "title,body,url,state,headRefName,labels")
	output, err := cmd.Output()
	if err != nil {
		return nil, wrapGHError("fetch pull request", err)
	}

	var resp ghPRSourceResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("parse pull request response: %w", err)
	}

	item := &SourceItem{
		Ref:        ref,
		Title:      resp.Title,
		Body:       resp.Body,
		URL:        resp.URL,
		State:      resp.State,
		HeadBranch: resp.HeadRefName,
	}
	for _, l := range resp.Labels {
		if l.Name != "" {
			item.Labels = append(item.Labels, l.Name)
		}
	}
	return item, nil
}

// ghDiscussionResponse mirrors the GraphQL shape returned for a discussion query.
type ghDiscussionResponse struct {
	Data struct {
		Repository struct {
			Discussion struct {
				Title  string `json:"title"`
				Body   string `json:"body"`
				URL    string `json:"url"`
				Labels struct {
					Nodes []struct {
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"labels"`
			} `json:"discussion"`
		} `json:"repository"`
	} `json:"data"`
}

// fetchDiscussion is best-effort: the gh CLI has no native discussion view, so
// we query the GraphQL API directly.
func fetchDiscussion(ctx context.Context, ref SourceRef) (*SourceItem, error) {
	const query = `query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){discussion(number:$number){title body url labels(first:20){nodes{name}}}}}`

	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+query,
		"-F", "owner="+ref.Owner,
		"-F", "repo="+ref.Repo,
		"-F", "number="+strconv.Itoa(ref.Number))
	output, err := cmd.Output()
	if err != nil {
		return nil, wrapGHError("fetch discussion", err)
	}

	var resp ghDiscussionResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("parse discussion response: %w", err)
	}

	d := resp.Data.Repository.Discussion
	if d.Title == "" && d.Body == "" {
		return nil, fmt.Errorf("discussion #%d not found in %s", ref.Number, ref.NameWithOwner())
	}

	item := &SourceItem{
		Ref:   ref,
		Title: d.Title,
		Body:  d.Body,
		URL:   d.URL,
	}
	for _, l := range d.Labels.Nodes {
		if l.Name != "" {
			item.Labels = append(item.Labels, l.Name)
		}
	}
	return item, nil
}

// remoteSlugPattern extracts "owner/repo" from common GitHub remote URL forms:
// https://github.com/owner/repo(.git), git@github.com:owner/repo(.git), and
// ssh://git@github.com/owner/repo(.git).
var remoteSlugPattern = regexp.MustCompile(`github\.com[:/]+([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

// RepoSlugFromRemote returns the "owner/repo" slug for a git remote URL, or an
// empty string if the URL does not point at github.com.
func RepoSlugFromRemote(remoteURL string) string {
	m := remoteSlugPattern.FindStringSubmatch(strings.TrimSpace(remoteURL))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// wrapGHError annotates a gh CLI error with its stderr output when available.
func wrapGHError(action string, err error) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr != "" {
			return fmt.Errorf("%s: %s", action, stderr)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}
