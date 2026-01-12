package ui

import (
	"regexp"
	"strconv"
	"strings"
)

// ParsedURL contains information extracted from a pasted URL
type ParsedURL struct {
	OriginalURL string
	Type        string // "github_pr", "github_issue", "linear"
	Title       string // Extracted or generated title
	PRURL       string // For GitHub PRs
	PRNumber    int    // For GitHub PRs
	IssueNumber int    // For GitHub issues or Linear issues
	IssueID     string // For Linear (e.g., "PROJ-123")
}

var (
	// GitHub PR URL: https://github.com/owner/repo/pull/123
	githubPRRegex = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/pull/(\d+)(?:[/?#].*)?$`)

	// GitHub Issue URL: https://github.com/owner/repo/issues/123
	githubIssueRegex = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/issues/(\d+)(?:[/?#].*)?$`)

	// Linear URL: https://linear.app/team/issue/PROJ-123/issue-title
	linearRegex = regexp.MustCompile(`^https?://linear\.app/([^/]+)/issue/([A-Z]+-\d+)(?:/.*)?$`)
)

// ParseURL attempts to parse a pasted string and extract structured information
// Returns nil if the string is not a recognized URL pattern
func ParseURL(input string) *ParsedURL {
	input = strings.TrimSpace(input)

	// Try GitHub PR
	if matches := githubPRRegex.FindStringSubmatch(input); matches != nil {
		owner := matches[1]
		repo := matches[2]
		prNumber, _ := strconv.Atoi(matches[3])

		return &ParsedURL{
			OriginalURL: input,
			Type:        "github_pr",
			Title:       formatGitHubPRTitle(owner, repo, prNumber),
			PRURL:       normalizeGitHubURL(input),
			PRNumber:    prNumber,
		}
	}

	// Try GitHub Issue
	if matches := githubIssueRegex.FindStringSubmatch(input); matches != nil {
		owner := matches[1]
		repo := matches[2]
		issueNumber, _ := strconv.Atoi(matches[3])

		return &ParsedURL{
			OriginalURL: input,
			Type:        "github_issue",
			Title:       formatGitHubIssueTitle(owner, repo, issueNumber),
			IssueNumber: issueNumber,
		}
	}

	// Try Linear
	if matches := linearRegex.FindStringSubmatch(input); matches != nil {
		team := matches[1]
		issueID := matches[2]

		return &ParsedURL{
			OriginalURL: input,
			Type:        "linear",
			Title:       formatLinearTitle(team, issueID),
			IssueID:     issueID,
		}
	}

	return nil
}

// normalizeGitHubURL removes query params, fragments, and trailing slashes to get clean PR URL
func normalizeGitHubURL(url string) string {
	// Extract base URL without query params or fragments
	if idx := strings.Index(url, "?"); idx != -1 {
		url = url[:idx]
	}
	if idx := strings.Index(url, "#"); idx != -1 {
		url = url[:idx]
	}
	// Remove trailing slash
	url = strings.TrimSuffix(url, "/")
	return url
}

func formatGitHubPRTitle(owner, repo string, prNumber int) string {
	return repo + " #" + strconv.Itoa(prNumber)
}

func formatGitHubIssueTitle(owner, repo string, issueNumber int) string {
	return repo + " #" + strconv.Itoa(issueNumber)
}

func formatLinearTitle(team, issueID string) string {
	return issueID
}
