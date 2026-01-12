package ui

import (
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *ParsedURL
	}{
		// GitHub PR tests
		{
			name:  "GitHub PR basic",
			input: "https://github.com/owner/repo/pull/123",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/pull/123",
				Type:        "github_pr",
				Title:       "repo #123",
				PRURL:       "https://github.com/owner/repo/pull/123",
				PRNumber:    123,
			},
		},
		{
			name:  "GitHub PR with query params",
			input: "https://github.com/owner/repo/pull/456?tab=files",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/pull/456?tab=files",
				Type:        "github_pr",
				Title:       "repo #456",
				PRURL:       "https://github.com/owner/repo/pull/456",
				PRNumber:    456,
			},
		},
		{
			name:  "GitHub PR with fragment",
			input: "https://github.com/owner/repo/pull/789#issuecomment-123456",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/pull/789#issuecomment-123456",
				Type:        "github_pr",
				Title:       "repo #789",
				PRURL:       "https://github.com/owner/repo/pull/789",
				PRNumber:    789,
			},
		},
		{
			name:  "GitHub PR http (not https)",
			input: "http://github.com/owner/repo/pull/999",
			expected: &ParsedURL{
				OriginalURL: "http://github.com/owner/repo/pull/999",
				Type:        "github_pr",
				Title:       "repo #999",
				PRURL:       "http://github.com/owner/repo/pull/999",
				PRNumber:    999,
			},
		},
		{
			name:  "GitHub PR with trailing slash",
			input: "https://github.com/owner/repo/pull/111/",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/pull/111/",
				Type:        "github_pr",
				Title:       "repo #111",
				PRURL:       "https://github.com/owner/repo/pull/111",
				PRNumber:    111,
			},
		},
		// GitHub Issue tests
		{
			name:  "GitHub Issue basic",
			input: "https://github.com/owner/repo/issues/42",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/issues/42",
				Type:        "github_issue",
				Title:       "repo #42",
				IssueNumber: 42,
			},
		},
		{
			name:  "GitHub Issue with query params",
			input: "https://github.com/owner/repo/issues/99?q=is%3Aopen",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/issues/99?q=is%3Aopen",
				Type:        "github_issue",
				Title:       "repo #99",
				IssueNumber: 99,
			},
		},
		// Linear tests
		{
			name:  "Linear basic",
			input: "https://linear.app/myteam/issue/PROJ-123",
			expected: &ParsedURL{
				OriginalURL: "https://linear.app/myteam/issue/PROJ-123",
				Type:        "linear",
				Title:       "PROJ-123",
				IssueID:     "PROJ-123",
			},
		},
		{
			name:  "Linear with issue title",
			input: "https://linear.app/myteam/issue/PROJ-456/fix-the-bug",
			expected: &ParsedURL{
				OriginalURL: "https://linear.app/myteam/issue/PROJ-456/fix-the-bug",
				Type:        "linear",
				Title:       "PROJ-456",
				IssueID:     "PROJ-456",
			},
		},
		{
			name:  "Linear with longer issue title",
			input: "https://linear.app/workflow/issue/WF-789/implement-magic-paste-functionality",
			expected: &ParsedURL{
				OriginalURL: "https://linear.app/workflow/issue/WF-789/implement-magic-paste-functionality",
				Type:        "linear",
				Title:       "WF-789",
				IssueID:     "WF-789",
			},
		},
		// Non-matching tests
		{
			name:     "Regular text",
			input:    "just some regular text",
			expected: nil,
		},
		{
			name:     "GitHub but not PR or issue",
			input:    "https://github.com/owner/repo",
			expected: nil,
		},
		{
			name:     "GitHub PR malformed (no number)",
			input:    "https://github.com/owner/repo/pull/",
			expected: nil,
		},
		{
			name:     "Random URL",
			input:    "https://example.com/some/path",
			expected: nil,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "Whitespace only",
			input:    "   ",
			expected: nil,
		},
		{
			name:  "GitHub PR with whitespace",
			input: "  https://github.com/owner/repo/pull/123  ",
			expected: &ParsedURL{
				OriginalURL: "https://github.com/owner/repo/pull/123",
				Type:        "github_pr",
				Title:       "repo #123",
				PRURL:       "https://github.com/owner/repo/pull/123",
				PRNumber:    123,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseURL(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("Expected %+v, got nil", tt.expected)
				return
			}

			if result.OriginalURL != tt.expected.OriginalURL {
				t.Errorf("OriginalURL: expected %q, got %q", tt.expected.OriginalURL, result.OriginalURL)
			}
			if result.Type != tt.expected.Type {
				t.Errorf("Type: expected %q, got %q", tt.expected.Type, result.Type)
			}
			if result.Title != tt.expected.Title {
				t.Errorf("Title: expected %q, got %q", tt.expected.Title, result.Title)
			}
			if result.PRURL != tt.expected.PRURL {
				t.Errorf("PRURL: expected %q, got %q", tt.expected.PRURL, result.PRURL)
			}
			if result.PRNumber != tt.expected.PRNumber {
				t.Errorf("PRNumber: expected %d, got %d", tt.expected.PRNumber, result.PRNumber)
			}
			if result.IssueNumber != tt.expected.IssueNumber {
				t.Errorf("IssueNumber: expected %d, got %d", tt.expected.IssueNumber, result.IssueNumber)
			}
			if result.IssueID != tt.expected.IssueID {
				t.Errorf("IssueID: expected %q, got %q", tt.expected.IssueID, result.IssueID)
			}
		})
	}
}

func TestNormalizeGitHubURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL with query params",
			input:    "https://github.com/owner/repo/pull/123?tab=files",
			expected: "https://github.com/owner/repo/pull/123",
		},
		{
			name:     "URL with fragment",
			input:    "https://github.com/owner/repo/pull/123#issuecomment-456",
			expected: "https://github.com/owner/repo/pull/123",
		},
		{
			name:     "URL with both query and fragment",
			input:    "https://github.com/owner/repo/pull/123?tab=files#top",
			expected: "https://github.com/owner/repo/pull/123",
		},
		{
			name:     "URL without query or fragment",
			input:    "https://github.com/owner/repo/pull/123",
			expected: "https://github.com/owner/repo/pull/123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeGitHubURL(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}
