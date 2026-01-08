package ui

import (
	"errors"
	"testing"
)

func TestListThemes(t *testing.T) {
	themes := ListThemes()
	if len(themes) != 5 {
		t.Errorf("expected 5 themes, got %d", len(themes))
	}

	// Check expected themes are present
	expected := map[string]bool{
		"onedark":    true,
		"nord":       true,
		"gruvbox":    true,
		"catppuccin": true,
		"default":    true,
	}
	for _, theme := range themes {
		if !expected[theme] {
			t.Errorf("unexpected theme: %s", theme)
		}
	}
}

func TestSetTheme(t *testing.T) {
	// Reset to default after test
	defer SetTheme("onedark")

	// Test setting valid themes
	for _, name := range ListThemes() {
		if err := SetTheme(name); err != nil {
			t.Errorf("SetTheme(%q) failed: %v", name, err)
		}
		if CurrentTheme().Name != name {
			t.Errorf("CurrentTheme().Name = %q, want %q", CurrentTheme().Name, name)
		}
	}

	// Test setting invalid theme
	if err := SetTheme("nonexistent"); err == nil {
		t.Error("SetTheme(nonexistent) should fail")
	}
}

func TestCurrentTheme(t *testing.T) {
	// Default theme should be OneDark
	theme := CurrentTheme()
	if theme.Name != "onedark" {
		t.Errorf("default theme should be onedark, got %q", theme.Name)
	}

	// Verify theme has required colors
	if theme.Primary == "" {
		t.Error("theme.Primary should not be empty")
	}
	if theme.Secondary == "" {
		t.Error("theme.Secondary should not be empty")
	}
	if theme.Muted == "" {
		t.Error("theme.Muted should not be empty")
	}
}

func TestLoadThemeFromDB(t *testing.T) {
	// Reset to default after test
	defer SetTheme("onedark")

	tests := []struct {
		name        string
		getSetting  func(string) (string, error)
		wantTheme   string
	}{
		{
			name: "loads saved theme",
			getSetting: func(key string) (string, error) {
				if key == "theme" {
					return "nord", nil
				}
				return "", nil
			},
			wantTheme: "nord",
		},
		{
			name: "uses default on error",
			getSetting: func(key string) (string, error) {
				return "", errors.New("db error")
			},
			wantTheme: "onedark",
		},
		{
			name: "uses default on empty",
			getSetting: func(key string) (string, error) {
				return "", nil
			},
			wantTheme: "onedark",
		},
		{
			name: "uses default on invalid theme",
			getSetting: func(key string) (string, error) {
				return "invalid_theme", nil
			},
			wantTheme: "onedark",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset to onedark first
			SetTheme("onedark")
			
			LoadThemeFromDB(tt.getSetting)
			if CurrentTheme().Name != tt.wantTheme {
				t.Errorf("got theme %q, want %q", CurrentTheme().Name, tt.wantTheme)
			}
		})
	}
}

func TestThemeColors(t *testing.T) {
	// Test that all themes have valid color values
	for name, theme := range BuiltinThemes {
		t.Run(name, func(t *testing.T) {
			// Check core colors
			if theme.Primary == "" {
				t.Error("Primary color is empty")
			}
			if theme.Secondary == "" {
				t.Error("Secondary color is empty")
			}
			if theme.Muted == "" {
				t.Error("Muted color is empty")
			}

			// Check semantic colors
			if theme.Success == "" {
				t.Error("Success color is empty")
			}
			if theme.Warning == "" {
				t.Error("Warning color is empty")
			}
			if theme.Error == "" {
				t.Error("Error color is empty")
			}

			// Check status colors
			if theme.InProgress == "" {
				t.Error("InProgress color is empty")
			}
			if theme.Done == "" {
				t.Error("Done color is empty")
			}
			if theme.Blocked == "" {
				t.Error("Blocked color is empty")
			}

			// Check card colors
			if theme.CardBg == "" {
				t.Error("CardBg color is empty")
			}
			if theme.CardFg == "" {
				t.Error("CardFg color is empty")
			}
		})
	}
}

func TestSetThemeFromJSON(t *testing.T) {
	// Reset to default after test
	defer SetTheme("onedark")

	validJSON := `{
		"name": "custom",
		"primary": "#FF0000",
		"secondary": "#00FF00",
		"muted": "#808080",
		"success": "#00FF00",
		"warning": "#FFFF00",
		"error": "#FF0000",
		"in_progress": "#FFA500",
		"done": "#00FF00",
		"blocked": "#FF0000",
		"backlog": "#808080",
		"card_bg": "#333333",
		"card_fg": "#FFFFFF",
		"card_border": "#666666",
		"card_border_hi": "#FF0000",
		"column_border": "#666666",
		"column_border_hi": "#FF0000"
	}`

	if err := SetThemeFromJSON(validJSON); err != nil {
		t.Errorf("SetThemeFromJSON failed: %v", err)
	}
	if CurrentTheme().Name != "custom" {
		t.Errorf("got theme %q, want custom", CurrentTheme().Name)
	}
	if CurrentTheme().Primary != "#FF0000" {
		t.Errorf("got primary %q, want #FF0000", CurrentTheme().Primary)
	}

	// Test invalid JSON
	if err := SetThemeFromJSON("not json"); err == nil {
		t.Error("SetThemeFromJSON should fail on invalid JSON")
	}
}
