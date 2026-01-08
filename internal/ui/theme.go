// Package ui provides the terminal user interface.
package ui

import (
	"encoding/json"
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Theme defines all colors used in the UI.
type Theme struct {
	Name string `json:"name"`

	// Core colors
	Primary   string `json:"primary"`   // Main accent color (selections, highlights)
	Secondary string `json:"secondary"` // Secondary accent (links, tags)
	Muted     string `json:"muted"`     // Dimmed text, borders

	// Semantic colors
	Success string `json:"success"` // Success states, done
	Warning string `json:"warning"` // Warning states
	Error   string `json:"error"`   // Error states, blocked

	// Status colors
	InProgress string `json:"in_progress"` // Queued/processing tasks
	Done       string `json:"done"`        // Completed tasks
	Blocked    string `json:"blocked"`     // Blocked tasks
	Backlog    string `json:"backlog"`     // Backlog tasks

	// Card colors
	CardBg         string `json:"card_bg"`          // Selected card background
	CardFg         string `json:"card_fg"`          // Selected card foreground
	CardBorder     string `json:"card_border"`      // Card border
	CardBorderHi   string `json:"card_border_hi"`   // Highlighted card border
	ColumnBorder   string `json:"column_border"`    // Column border
	ColumnBorderHi string `json:"column_border_hi"` // Selected column border
}

// BuiltinThemes contains all built-in themes.
var BuiltinThemes = map[string]Theme{
	"onedark": OneDarkTheme,
	"default": DefaultTheme,
	"nord":    NordTheme,
	"gruvbox": GruvboxTheme,
	"catppuccin": CatppuccinTheme,
}

// OneDarkTheme is inspired by Atom's One Dark theme - subtle and easy on the eyes.
var OneDarkTheme = Theme{
	Name: "onedark",

	// Core - muted purples and cyans
	Primary:   "#61AFEF", // Soft blue
	Secondary: "#56B6C2", // Cyan
	Muted:     "#5C6370", // Comment gray

	// Semantic
	Success: "#98C379", // Green
	Warning: "#E5C07B", // Yellow
	Error:   "#E06C75", // Red

	// Status
	InProgress: "#D19A66", // Orange (subtle)
	Done:       "#98C379", // Green
	Blocked:    "#E06C75", // Red
	Backlog:    "#5C6370", // Gray

	// Cards
	CardBg:         "#3E4451", // Slightly lighter than bg
	CardFg:         "#ABB2BF", // Foreground
	CardBorder:     "#3E4451", // Subtle border
	CardBorderHi:   "#61AFEF", // Blue highlight
	ColumnBorder:   "#3E4451", // Column border
	ColumnBorderHi: "#61AFEF", // Selected column
}

// DefaultTheme is the original purple-heavy theme.
var DefaultTheme = Theme{
	Name: "default",

	// Core
	Primary:   "#7C3AED", // Purple
	Secondary: "#06B6D4", // Cyan
	Muted:     "#6B7280", // Gray

	// Semantic
	Success: "#10B981", // Green
	Warning: "#F59E0B", // Amber
	Error:   "#EF4444", // Red

	// Status
	InProgress: "#9A9463", // Muted olive
	Done:       "#10B981", // Green
	Blocked:    "#EF4444", // Red
	Backlog:    "#6B7280", // Gray

	// Cards
	CardBg:         "#333333",
	CardFg:         "#FFFFFF",
	CardBorder:     "#6B7280",
	CardBorderHi:   "#7C3AED",
	ColumnBorder:   "#6B7280",
	ColumnBorderHi: "#7C3AED",
}

// NordTheme is inspired by the Nord color palette - arctic, bluish tones.
var NordTheme = Theme{
	Name: "nord",

	// Core
	Primary:   "#88C0D0", // Nord8 - frost
	Secondary: "#81A1C1", // Nord9 - frost
	Muted:     "#4C566A", // Nord3 - polar night

	// Semantic
	Success: "#A3BE8C", // Nord14 - aurora green
	Warning: "#EBCB8B", // Nord13 - aurora yellow
	Error:   "#BF616A", // Nord11 - aurora red

	// Status
	InProgress: "#D08770", // Nord12 - aurora orange
	Done:       "#A3BE8C", // Nord14
	Blocked:    "#BF616A", // Nord11
	Backlog:    "#4C566A", // Nord3

	// Cards
	CardBg:         "#3B4252", // Nord1
	CardFg:         "#ECEFF4", // Nord6
	CardBorder:     "#4C566A", // Nord3
	CardBorderHi:   "#88C0D0", // Nord8
	ColumnBorder:   "#4C566A", // Nord3
	ColumnBorderHi: "#88C0D0", // Nord8
}

// GruvboxTheme is inspired by the Gruvbox color scheme - retro, earthy tones.
var GruvboxTheme = Theme{
	Name: "gruvbox",

	// Core
	Primary:   "#83A598", // Aqua
	Secondary: "#B8BB26", // Green
	Muted:     "#665C54", // Gray

	// Semantic
	Success: "#B8BB26", // Green
	Warning: "#FABD2F", // Yellow
	Error:   "#FB4934", // Red

	// Status
	InProgress: "#FE8019", // Orange
	Done:       "#B8BB26", // Green
	Blocked:    "#FB4934", // Red
	Backlog:    "#665C54", // Gray

	// Cards
	CardBg:         "#3C3836", // Dark bg
	CardFg:         "#EBDBB2", // Light fg
	CardBorder:     "#504945", // Medium
	CardBorderHi:   "#83A598", // Aqua
	ColumnBorder:   "#504945", // Medium
	ColumnBorderHi: "#83A598", // Aqua
}

// CatppuccinTheme is inspired by Catppuccin Mocha - soothing pastel colors.
var CatppuccinTheme = Theme{
	Name: "catppuccin",

	// Core
	Primary:   "#CBA6F7", // Mauve
	Secondary: "#89DCEB", // Sky
	Muted:     "#6C7086", // Overlay0

	// Semantic
	Success: "#A6E3A1", // Green
	Warning: "#F9E2AF", // Yellow
	Error:   "#F38BA8", // Red

	// Status
	InProgress: "#FAB387", // Peach
	Done:       "#A6E3A1", // Green
	Blocked:    "#F38BA8", // Red
	Backlog:    "#6C7086", // Overlay0

	// Cards
	CardBg:         "#313244", // Surface0
	CardFg:         "#CDD6F4", // Text
	CardBorder:     "#45475A", // Surface1
	CardBorderHi:   "#CBA6F7", // Mauve
	ColumnBorder:   "#45475A", // Surface1
	ColumnBorderHi: "#CBA6F7", // Mauve
}

// currentTheme is the active theme (defaults to OneDark).
var currentTheme = OneDarkTheme

// CurrentTheme returns the current theme.
func CurrentTheme() Theme {
	return currentTheme
}

// SetTheme sets the current theme by name.
func SetTheme(name string) error {
	theme, ok := BuiltinThemes[name]
	if !ok {
		return fmt.Errorf("unknown theme: %s", name)
	}
	currentTheme = theme
	refreshStyles()
	return nil
}

// SetThemeFromJSON sets a custom theme from JSON.
func SetThemeFromJSON(data string) error {
	var theme Theme
	if err := json.Unmarshal([]byte(data), &theme); err != nil {
		return fmt.Errorf("parse theme: %w", err)
	}
	if theme.Name == "" {
		theme.Name = "custom"
	}
	currentTheme = theme
	refreshStyles()
	return nil
}

// ThemeToJSON serializes a theme to JSON.
func ThemeToJSON(theme Theme) (string, error) {
	data, err := json.MarshalIndent(theme, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ListThemes returns the names of all built-in themes.
func ListThemes() []string {
	names := make([]string, 0, len(BuiltinThemes))
	for name := range BuiltinThemes {
		names = append(names, name)
	}
	return names
}

// refreshStyles updates all lipgloss styles after a theme change.
func refreshStyles() {
	t := currentTheme

	// Update color variables
	ColorPrimary = lipgloss.Color(t.Primary)
	ColorSecondary = lipgloss.Color(t.Secondary)
	ColorSuccess = lipgloss.Color(t.Success)
	ColorWarning = lipgloss.Color(t.Warning)
	ColorError = lipgloss.Color(t.Error)
	ColorMuted = lipgloss.Color(t.Muted)
	ColorInProgress = lipgloss.Color(t.InProgress)
	ColorDone = lipgloss.Color(t.Done)
	ColorBlocked = lipgloss.Color(t.Blocked)

	// Update text styles
	Dim = lipgloss.NewStyle().Foreground(ColorMuted)
	Title = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	Subtitle = lipgloss.NewStyle().Foreground(ColorSecondary)
	Success = lipgloss.NewStyle().Foreground(ColorSuccess)
	Warning = lipgloss.NewStyle().Foreground(ColorWarning)
	Error = lipgloss.NewStyle().Foreground(ColorError)

	// Update status badges
	StatusInProgress = lipgloss.NewStyle().
		Foreground(ColorInProgress).
		SetString("⋯")
	StatusDone = lipgloss.NewStyle().
		Foreground(ColorDone).
		SetString("✓")
	StatusBlocked = lipgloss.NewStyle().
		Foreground(ColorBlocked).
		SetString("!")

	// Update priority
	PriorityHigh = lipgloss.NewStyle().
		Foreground(ColorError).
		SetString("↑")

	// Update box styles
	Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorMuted).
		Padding(0, 1)

	FocusedBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1)

	// Update list styles
	ListItem = lipgloss.NewStyle().
		PaddingLeft(2)

	SelectedListItem = lipgloss.NewStyle().
		PaddingLeft(2).
		Foreground(ColorPrimary).
		Bold(true)

	// Update help styles
	HelpBar = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Padding(1, 0)

	HelpKey = lipgloss.NewStyle().
		Foreground(ColorSecondary).
		Bold(true)

	HelpDesc = lipgloss.NewStyle().
		Foreground(ColorMuted)

	// Update header
	Header = lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Padding(0, 1).
		MarginBottom(1)

	// Update tag styles
	ProjectTag = lipgloss.NewStyle().
		Foreground(ColorSecondary).
		Bold(true)

	TypeTag = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Italic(true)
}

// GetThemeCardColors returns card-specific colors from the current theme.
func GetThemeCardColors() (bg, fg lipgloss.Color) {
	return lipgloss.Color(currentTheme.CardBg), lipgloss.Color(currentTheme.CardFg)
}

// GetThemeBorderColors returns border colors from the current theme.
func GetThemeBorderColors() (normal, highlighted lipgloss.Color) {
	return lipgloss.Color(currentTheme.ColumnBorder), lipgloss.Color(currentTheme.ColumnBorderHi)
}
