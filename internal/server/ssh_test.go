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
			environ: []string{"HOME=/home/user", "TASK_CWD=/Users/test/Projects/myproject", "SHELL=/bin/bash"},
			key:     "TASK_CWD",
			want:    "/Users/test/Projects/myproject",
		},
		{
			name:    "not found",
			environ: []string{"HOME=/home/user", "SHELL=/bin/bash"},
			key:     "TASK_CWD",
			want:    "",
		},
		{
			name:    "empty environ",
			environ: []string{},
			key:     "TASK_CWD",
			want:    "",
		},
		{
			name:    "nil environ",
			environ: nil,
			key:     "TASK_CWD",
			want:    "",
		},
		{
			name:    "empty value",
			environ: []string{"TASK_CWD="},
			key:     "TASK_CWD",
			want:    "",
		},
		{
			name:    "value with equals sign",
			environ: []string{"TASK_CWD=/path/with=equals"},
			key:     "TASK_CWD",
			want:    "/path/with=equals",
		},
		{
			name:    "partial key match should not match",
			environ: []string{"TASK_CWD_EXTRA=/wrong"},
			key:     "TASK_CWD",
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
