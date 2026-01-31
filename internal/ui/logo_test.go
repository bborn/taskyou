package ui

import (
	"os"
	"strings"
	"testing"
)

func TestRenderTaskyHeader(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		expectEmpty bool
	}{
		{name: "narrow", width: 15, expectEmpty: true},
		{name: "normal", width: 80, expectEmpty: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderTaskyHeader(tt.width)
			if tt.expectEmpty && got != "" {
				t.Errorf("want empty, got %q", got)
			}
			if !tt.expectEmpty && got == "" {
				t.Error("want non-empty")
			}
		})
	}
}

func TestRenderTaskyCompact(t *testing.T) {
	got := RenderTaskyCompact()
	if got == "" {
		t.Error("empty")
	}
	if strings.Contains(got, "\n") {
		t.Error("should be single line")
	}
}

func TestRenderTaskyFull(t *testing.T) {
	got := RenderTaskyFull()
	if !strings.Contains(got, "TaskYou") {
		t.Error("missing brand")
	}
}

func TestRenderTaskyWithTagline(t *testing.T) {
	got := RenderTaskyWithTagline()
	if !strings.Contains(got, "supercharged") {
		t.Error("missing tagline")
	}
}

func TestDetectImageSupport(t *testing.T) {
	// Save and restore env
	origTermProg := os.Getenv("TERM_PROGRAM")
	origKitty := os.Getenv("KITTY_WINDOW_ID")
	origTerm := os.Getenv("TERM")
	origLCTerm := os.Getenv("LC_TERMINAL")
	defer func() {
		os.Setenv("TERM_PROGRAM", origTermProg)
		os.Setenv("TERM", origTerm)
		os.Setenv("LC_TERMINAL", origLCTerm)
		if origKitty == "" {
			os.Unsetenv("KITTY_WINDOW_ID")
		} else {
			os.Setenv("KITTY_WINDOW_ID", origKitty)
		}
	}()

	// Test iTerm2 detection
	os.Setenv("TERM_PROGRAM", "iTerm.app")
	os.Setenv("TERM", "xterm-256color")
	os.Unsetenv("KITTY_WINDOW_ID")
	os.Unsetenv("LC_TERMINAL")
	if DetectImageSupport() != ImageSupportITerm2 {
		t.Error("should detect iTerm2")
	}

	// Test Kitty detection
	os.Unsetenv("TERM_PROGRAM")
	os.Unsetenv("LC_TERMINAL")
	os.Setenv("TERM", "xterm-kitty")
	os.Unsetenv("KITTY_WINDOW_ID")
	if DetectImageSupport() != ImageSupportKitty {
		t.Error("should detect Kitty")
	}

	// Test no support
	os.Unsetenv("TERM_PROGRAM")
	os.Unsetenv("LC_TERMINAL")
	os.Setenv("TERM", "xterm")
	os.Unsetenv("KITTY_WINDOW_ID")
	if DetectImageSupport() != ImageSupportNone {
		t.Error("should detect no support")
	}
}

func TestRenderInlineImage(t *testing.T) {
	// Save and restore env
	origTermProg := os.Getenv("TERM_PROGRAM")
	origTerm := os.Getenv("TERM")
	origLCTerm := os.Getenv("LC_TERMINAL")
	origKitty := os.Getenv("KITTY_WINDOW_ID")
	defer func() {
		os.Setenv("TERM_PROGRAM", origTermProg)
		os.Setenv("TERM", origTerm)
		os.Setenv("LC_TERMINAL", origLCTerm)
		if origKitty != "" {
			os.Setenv("KITTY_WINDOW_ID", origKitty)
		}
	}()

	// Without image support, should return empty
	os.Unsetenv("TERM_PROGRAM")
	os.Unsetenv("LC_TERMINAL")
	os.Setenv("TERM", "xterm")
	os.Unsetenv("KITTY_WINDOW_ID")

	got := RenderInlineImage([]byte("test"), 2, 1)
	if got != "" {
		t.Error("should be empty without terminal support")
	}

	// With iTerm2 support, should return escape sequence
	os.Setenv("TERM_PROGRAM", "iTerm.app")
	got = RenderInlineImage([]byte("test"), 2, 1)
	if !strings.HasPrefix(got, "\x1b]1337") {
		t.Error("should return iTerm2 escape sequence")
	}
}

func TestTaskyHeaderHeight(t *testing.T) {
	if TaskyHeaderHeight() != 1 {
		t.Error("should be 1 line")
	}
}

func TestLogoImageEmbedded(t *testing.T) {
	if len(logoImageData) == 0 {
		t.Error("logo image should be embedded")
	}
}
