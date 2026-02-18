package github

import "testing"

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer patch", "v0.1.0", "v0.1.1", true},
		{"newer minor", "v0.1.0", "v0.2.0", true},
		{"newer major", "v0.1.0", "v1.0.0", true},
		{"same version", "v0.1.0", "v0.1.0", false},
		{"older version", "v0.2.0", "v0.1.0", false},
		{"no v prefix", "0.1.0", "0.2.0", true},
		{"mixed prefix", "v0.1.0", "0.2.0", true},
		{"dev version", "dev", "v0.1.0", false},
		{"empty current", "", "v0.1.0", false},
		{"empty latest", "v0.1.0", "", false},
		{"pre-release latest", "v0.1.0", "v0.2.0-rc1", true},
		{"pre-release current", "v0.1.0-beta", "v0.1.1", true},
		{"multi-digit", "v1.9.0", "v1.10.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNewerVersion(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v0.0.0", true},
		{"v1.2.3-rc1", true},
		{"invalid", false},
		{"v1.2", false},
		{"v1.2.3.4", false},
		{"v1.a.3", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseVersion(tt.input)
			if tt.valid && result == nil {
				t.Errorf("parseVersion(%q) returned nil, expected valid", tt.input)
			}
			if !tt.valid && result != nil {
				t.Errorf("parseVersion(%q) returned %v, expected nil", tt.input, result)
			}
		})
	}
}
