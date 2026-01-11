package server

import "testing"

func TestGetEnvValue(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		key     string
		want    string
	}{
		{
			name:    "found",
			environ: []string{"HOME=/home/user", "WORKTREE_CWD=/Users/test/Projects/myproject", "SHELL=/bin/bash"},
			key:     "WORKTREE_CWD",
			want:    "/Users/test/Projects/myproject",
		},
		{
			name:    "not found",
			environ: []string{"HOME=/home/user", "SHELL=/bin/bash"},
			key:     "WORKTREE_CWD",
			want:    "",
		},
		{
			name:    "empty environ",
			environ: []string{},
			key:     "WORKTREE_CWD",
			want:    "",
		},
		{
			name:    "nil environ",
			environ: nil,
			key:     "WORKTREE_CWD",
			want:    "",
		},
		{
			name:    "empty value",
			environ: []string{"WORKTREE_CWD="},
			key:     "WORKTREE_CWD",
			want:    "",
		},
		{
			name:    "value with equals sign",
			environ: []string{"WORKTREE_CWD=/path/with=equals"},
			key:     "WORKTREE_CWD",
			want:    "/path/with=equals",
		},
		{
			name:    "partial key match should not match",
			environ: []string{"WORKTREE_CWD_EXTRA=/wrong"},
			key:     "WORKTREE_CWD",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetEnvValue(tt.environ, tt.key)
			if got != tt.want {
				t.Errorf("GetEnvValue() = %q, want %q", got, tt.want)
			}
		})
	}
}
