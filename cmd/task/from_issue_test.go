package main

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/github"
)

func TestMergeSourceBody(t *testing.T) {
	item := &github.SourceItem{
		Body: "Steps to reproduce:\n1. do a thing\n",
		URL:  "https://github.com/bborn/taskyou/issues/123",
	}

	t.Run("uses imported body when user body empty", func(t *testing.T) {
		got := mergeSourceBody("", item)
		want := "Steps to reproduce:\n1. do a thing\n\nSource: https://github.com/bborn/taskyou/issues/123"
		if got != want {
			t.Errorf("mergeSourceBody() = %q, want %q", got, want)
		}
	})

	t.Run("keeps user body and appends link", func(t *testing.T) {
		got := mergeSourceBody("my own notes", item)
		want := "my own notes\n\nSource: https://github.com/bborn/taskyou/issues/123"
		if got != want {
			t.Errorf("mergeSourceBody() = %q, want %q", got, want)
		}
	})

	t.Run("always links back to source", func(t *testing.T) {
		empty := &github.SourceItem{URL: "https://github.com/bborn/taskyou/pull/9"}
		got := mergeSourceBody("", empty)
		if !strings.Contains(got, "Source: https://github.com/bborn/taskyou/pull/9") {
			t.Errorf("mergeSourceBody() = %q, missing source link", got)
		}
	})
}

func TestMergeSourceTags(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		labels   []string
		want     string
	}{
		{"labels only", "", []string{"bug", "ui"}, "bug,ui"},
		{"existing only", "urgent", nil, "urgent"},
		{"merge", "urgent", []string{"bug", "ui"}, "urgent,bug,ui"},
		{"dedupes case-insensitively", "Bug", []string{"bug", "ui"}, "Bug,ui"},
		{"trims whitespace", " a , b ", []string{" c "}, "a,b,c"},
		{"drops empties", ",,", []string{"", "x"}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeSourceTags(tt.existing, tt.labels); got != tt.want {
				t.Errorf("mergeSourceTags(%q, %v) = %q, want %q", tt.existing, tt.labels, got, tt.want)
			}
		})
	}
}
