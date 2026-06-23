package github

import "testing"

func TestParseSourceURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    SourceRef
		wantErr bool
	}{
		{
			name: "issue",
			url:  "https://github.com/bborn/taskyou/issues/123",
			want: SourceRef{Owner: "bborn", Repo: "taskyou", Kind: SourceIssue, Number: 123},
		},
		{
			name: "pull request",
			url:  "https://github.com/bborn/taskyou/pull/45",
			want: SourceRef{Owner: "bborn", Repo: "taskyou", Kind: SourcePR, Number: 45},
		},
		{
			name: "discussion",
			url:  "https://github.com/bborn/taskyou/discussions/7",
			want: SourceRef{Owner: "bborn", Repo: "taskyou", Kind: SourceDiscussion, Number: 7},
		},
		{
			name: "issue with comment anchor",
			url:  "https://github.com/bborn/taskyou/issues/123#issuecomment-456",
			want: SourceRef{Owner: "bborn", Repo: "taskyou", Kind: SourceIssue, Number: 123},
		},
		{
			name: "http scheme and surrounding whitespace",
			url:  "  http://github.com/octo-org/my.repo/pull/9  ",
			want: SourceRef{Owner: "octo-org", Repo: "my.repo", Kind: SourcePR, Number: 9},
		},
		{
			name:    "not a github url",
			url:     "https://gitlab.com/foo/bar/issues/1",
			wantErr: true,
		},
		{
			name:    "missing number",
			url:     "https://github.com/foo/bar/issues",
			wantErr: true,
		},
		{
			name:    "commits path is not supported",
			url:     "https://github.com/foo/bar/commit/abc123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSourceURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSourceURL(%q) expected error, got %+v", tt.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSourceURL(%q) unexpected error: %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("ParseSourceURL(%q) = %+v, want %+v", tt.url, got, tt.want)
			}
		})
	}
}

func TestSourceRefNameWithOwner(t *testing.T) {
	ref := SourceRef{Owner: "bborn", Repo: "taskyou"}
	if got := ref.NameWithOwner(); got != "bborn/taskyou" {
		t.Errorf("NameWithOwner() = %q, want %q", got, "bborn/taskyou")
	}
}

func TestRepoSlugFromRemote(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"https://github.com/bborn/taskyou.git", "bborn/taskyou"},
		{"https://github.com/bborn/taskyou", "bborn/taskyou"},
		{"git@github.com:bborn/taskyou.git", "bborn/taskyou"},
		{"ssh://git@github.com/bborn/taskyou.git", "bborn/taskyou"},
		{"https://github.com/bborn/taskyou/\n", "bborn/taskyou"},
		{"git@gitlab.com:bborn/taskyou.git", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := RepoSlugFromRemote(tt.remote); got != tt.want {
			t.Errorf("RepoSlugFromRemote(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}
