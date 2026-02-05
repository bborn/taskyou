package github

import (
	"testing"
)

func TestIssueInfoStructure(t *testing.T) {
	// Verify IssueInfo struct can be created
	info := IssueInfo{
		Number: 123,
		URL:    "https://github.com/test/repo/issues/123",
		Title:  "Test Issue",
	}

	if info.Number != 123 {
		t.Errorf("IssueInfo.Number = %d, want 123", info.Number)
	}
	if info.URL != "https://github.com/test/repo/issues/123" {
		t.Errorf("IssueInfo.URL = %s, want https://github.com/test/repo/issues/123", info.URL)
	}
	if info.Title != "Test Issue" {
		t.Errorf("IssueInfo.Title = %s, want Test Issue", info.Title)
	}
}

func TestIsGitHubRepoNoGh(t *testing.T) {
	// If gh CLI is not available or not in a github repo, should return false
	// This test just verifies the function doesn't panic
	result := IsGitHubRepo("/nonexistent/path")
	// We expect false since the path doesn't exist
	if result {
		t.Error("IsGitHubRepo should return false for nonexistent path")
	}
}

func TestCreateIssueNoGh(t *testing.T) {
	// Test that CreateIssue returns an error when not in a valid repo
	// This test uses a nonexistent path to ensure failure
	_, err := CreateIssue("/nonexistent/path", "Test", "Body")
	if err == nil {
		t.Error("CreateIssue should return an error for nonexistent path")
	}
}

func TestCloseIssueNoGh(t *testing.T) {
	// Test that CloseIssue returns an error when not in a valid repo
	err := CloseIssue("/nonexistent/path", 123)
	if err == nil {
		t.Error("CloseIssue should return an error for nonexistent path")
	}
}
