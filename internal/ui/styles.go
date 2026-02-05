// Package ui provides the terminal user interface.
package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
	"github.com/charmbracelet/lipgloss"
)

// unicodeSupported caches whether the terminal supports Unicode.
// Initialized once on first call to SupportsUnicode().
var (
	unicodeSupported     bool
	unicodeSupportedOnce sync.Once
)

// SupportsUnicode returns true if the terminal likely supports Unicode characters.
// It checks LANG, LC_ALL, and LC_CTYPE environment variables for UTF-8 indicators.
func SupportsUnicode() bool {
	unicodeSupportedOnce.Do(func() {
		// Check common locale environment variables for UTF-8
		for _, envVar := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
			val := strings.ToLower(os.Getenv(envVar))
			if strings.Contains(val, "utf-8") || strings.Contains(val, "utf8") {
				unicodeSupported = true
				return
			}
		}
		// Default to false if no UTF-8 locale found
		unicodeSupported = false
	})
	return unicodeSupported
}

// Icon constants - Unicode and ASCII versions
const (
	// Unicode icons
	IconBacklogUnicode    = "◦"
	IconInProgressUnicode = "▶"
	IconBlockedUnicode    = "⚠"
	IconDoneUnicode    = "✓"
	IconPinUnicode     = "📌"
	IconWarningUnicode = "⚠"
	IconArrowUpUnicode    = "↑"
	IconArrowDownUnicode  = "↓"
	IconArrowLeftUnicode  = "←"
	IconArrowRightUnicode = "→"
	IconShiftUpUnicode    = "⇧↑"
	IconShiftDownUnicode  = "⇧↓"

	// ASCII fallbacks
	IconBacklogASCII     = "o"
	IconInProgressASCII  = ">"
	IconProcessingASCII  = "~"
	IconBlockedASCII     = "!"
	IconDoneASCII    = "*"
	IconPinASCII     = "P"
	IconWarningASCII = "!"
	IconArrowUpASCII     = "^"
	IconArrowDownASCII   = "v"
	IconArrowLeftASCII   = "<"
	IconArrowRightASCII  = ">"
	IconShiftUpASCII     = "^^"
	IconShiftDownASCII   = "vv"
	IconProcessingUnicode = "⋯"
	IconDefaultUnicode    = "·"
	IconDefaultASCII      = "."
)

// Icon returns the appropriate icon based on terminal Unicode support.
func Icon(unicodeIcon, asciiIcon string) string {
	if SupportsUnicode() {
		return unicodeIcon
	}
	return asciiIcon
}

// IconBacklog returns the backlog status icon.
func IconBacklog() string { return Icon(IconBacklogUnicode, IconBacklogASCII) }

// IconInProgress returns the in-progress status icon.
func IconInProgress() string { return Icon(IconInProgressUnicode, IconInProgressASCII) }

// IconBlocked returns the blocked status icon.
func IconBlocked() string { return Icon(IconBlockedUnicode, IconBlockedASCII) }

// IconDone returns the done status icon.
func IconDone() string { return Icon(IconDoneUnicode, IconDoneASCII) }

// IconPin returns the pin icon.
func IconPin() string { return Icon(IconPinUnicode, IconPinASCII) }

// IconArrowUp returns the up arrow.
func IconArrowUp() string { return Icon(IconArrowUpUnicode, IconArrowUpASCII) }

// IconArrowDown returns the down arrow.
func IconArrowDown() string { return Icon(IconArrowDownUnicode, IconArrowDownASCII) }

// IconArrowLeft returns the left arrow.
func IconArrowLeft() string { return Icon(IconArrowLeftUnicode, IconArrowLeftASCII) }

// IconArrowRight returns the right arrow.
func IconArrowRight() string { return Icon(IconArrowRightUnicode, IconArrowRightASCII) }

// IconShiftUp returns the shift+up icon.
func IconShiftUp() string { return Icon(IconShiftUpUnicode, IconShiftUpASCII) }

// IconShiftDown returns the shift+down icon.
func IconShiftDown() string { return Icon(IconShiftDownUnicode, IconShiftDownASCII) }

// IconProcessing returns the processing status icon.
func IconProcessing() string { return Icon(IconProcessingUnicode, IconProcessingASCII) }

// IconDefault returns the default/unknown status icon.
func IconDefault() string { return Icon(IconDefaultUnicode, IconDefaultASCII) }

// IsGlobalDangerousMode returns true if the system is running in global dangerous mode.
// This is set by the WORKTREE_DANGEROUS_MODE=1 environment variable.
func IsGlobalDangerousMode() bool {
	return os.Getenv("WORKTREE_DANGEROUS_MODE") == "1"
}

// Colors - these are updated by refreshStyles() when theme changes
var (
	// Primary colors
	ColorPrimary   = lipgloss.Color("#61AFEF") // Soft blue (OneDark default)
	ColorSecondary = lipgloss.Color("#56B6C2") // Cyan
	ColorSuccess   = lipgloss.Color("#98C379") // Green
	ColorWarning   = lipgloss.Color("#E5C07B") // Yellow
	ColorError     = lipgloss.Color("#E06C75") // Red
	ColorDangerous = lipgloss.Color("#E06C75") // Red (same as error, for dangerous mode indicator)
	ColorMuted     = lipgloss.Color("#5C6370") // Gray

	// Status colors
	ColorInProgress = lipgloss.Color("#D19A66") // Orange
	ColorDone       = lipgloss.Color("#98C379") // Green
	ColorBlocked    = lipgloss.Color("#E06C75") // Red

	// Default project colors palette - used when no color is set
	// These are distinct, visually pleasing colors for project labels
	DefaultProjectColors = []string{
		"#C678DD", // Purple
		"#61AFEF", // Blue
		"#56B6C2", // Cyan
		"#98C379", // Green
		"#E5C07B", // Yellow
		"#E06C75", // Red/Pink
		"#D19A66", // Orange
		"#ABB2BF", // Gray
	}

	// Type colors (fixed, not theme-dependent)
	ColorCode     = lipgloss.Color("#C678DD") // Purple
	ColorWriting  = lipgloss.Color("#E5C07B") // Yellow
	ColorThinking = lipgloss.Color("#56B6C2") // Cyan
)

// Base styles - these are updated by refreshStyles() when theme changes
var (
	// Text styles
	Bold           = lipgloss.NewStyle().Bold(true)
	Dim            = lipgloss.NewStyle().Foreground(ColorMuted)
	Title          = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	Subtitle       = lipgloss.NewStyle().Foreground(ColorSecondary)
	Success        = lipgloss.NewStyle().Foreground(ColorSuccess)
	Warning        = lipgloss.NewStyle().Foreground(ColorWarning)
	Error          = lipgloss.NewStyle().Foreground(ColorError)
	AttachmentChip = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorMuted).
			Foreground(ColorMuted).
			Padding(0, 1)
	AttachmentChipSelected = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Foreground(ColorPrimary).
				Bold(true).
				Padding(0, 1)

	// Status badges
	StatusInProgress = lipgloss.NewStyle().
				Foreground(ColorInProgress).
				SetString("⋯")
	StatusDone = lipgloss.NewStyle().
			Foreground(ColorDone).
			SetString("✓")
	StatusBlocked = lipgloss.NewStyle().
			Foreground(ColorBlocked).
			SetString("!")

	// Box styles
	Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorMuted).
		Padding(0, 1)

	FocusedBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1)

	// List item styles
	ListItem = lipgloss.NewStyle().
			PaddingLeft(2)

	SelectedListItem = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(ColorPrimary).
				Bold(true)

	// Help bar
	HelpBar = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Padding(1, 0)

	HelpKey = lipgloss.NewStyle().
		Foreground(ColorSecondary).
		Bold(true)

	HelpDesc = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Header
	Header = lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Padding(0, 1).
		MarginBottom(1)

	// Tag styles
	ProjectTag = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Bold(true)

	TypeTag = lipgloss.NewStyle().
		Foreground(ColorMuted).
		Italic(true)
)

// StatusStyle returns the style for a given status.
func StatusStyle(status string) lipgloss.Style {
	switch status {
	case "queued", "processing":
		return StatusInProgress
	case "done":
		return StatusDone
	case "blocked":
		return StatusBlocked
	default:
		return Dim
	}
}

// StatusIcon returns the icon for a given status.
func StatusIcon(status string) string {
	switch status {
	case "queued":
		return IconBacklog()
	case "processing":
		return IconProcessing()
	case "done":
		return IconDone()
	case "blocked":
		return IconBlocked()
	default:
		return IconDefault()
	}
}

// projectColorCache stores project colors loaded from the database.
// Access should be protected by projectColorMu.
var (
	projectColorCache = make(map[string]string)
	projectColorMu    sync.RWMutex
)

// SetProjectColors updates the project color cache with colors from the database.
// This should be called when projects are loaded.
func SetProjectColors(colors map[string]string) {
	projectColorMu.Lock()
	defer projectColorMu.Unlock()
	projectColorCache = colors
}

// SetProjectColor sets the color for a single project.
func SetProjectColor(project, color string) {
	projectColorMu.Lock()
	defer projectColorMu.Unlock()
	projectColorCache[project] = color
}

// GetDefaultProjectColor returns a default color for a project based on its index.
// Used when a project doesn't have a color set.
func GetDefaultProjectColor(index int) string {
	if index < 0 {
		index = 0
	}
	return DefaultProjectColors[index%len(DefaultProjectColors)]
}

// LoadProjectColors loads all project colors from the database into the cache.
func LoadProjectColors(database *db.DB) {
	projects, err := database.ListProjects()
	if err != nil {
		return
	}

	colors := make(map[string]string)
	for _, p := range projects {
		if p.Color != "" {
			colors[p.Name] = p.Color
		}
	}
	SetProjectColors(colors)
}

// ProjectColor returns the color for a project.
// It uses the cached color from the database, or falls back to a default color.
func ProjectColor(project string) lipgloss.Color {
	projectColorMu.RLock()
	if color, ok := projectColorCache[project]; ok && color != "" {
		projectColorMu.RUnlock()
		return lipgloss.Color(color)
	}
	projectColorMu.RUnlock()

	// Fallback to muted for unknown projects
	return ColorMuted
}

// StatusColor returns the background color for a status.
func StatusColor(status string) lipgloss.Color {
	switch status {
	case "queued", "processing":
		return ColorInProgress
	case "done":
		return ColorDone
	case "blocked":
		return ColorBlocked
	default:
		return ColorMuted
	}
}

// PR status colors
var (
	ColorPROpen    = lipgloss.Color("#98C379") // Green - ready to merge
	ColorPRDraft   = lipgloss.Color("#5C6370") // Gray - draft
	ColorPRMerged  = lipgloss.Color("#C678DD") // Purple - merged
	ColorPRClosed  = lipgloss.Color("#E06C75") // Red - closed
	ColorPRPending = lipgloss.Color("#E5C07B") // Yellow - checks running
	ColorPRFailing = lipgloss.Color("#E06C75") // Red - checks failing
)

// PRStatusBadge returns a styled badge for a PR status.
func PRStatusBadge(pr *github.PRInfo) string {
	if pr == nil {
		return ""
	}

	var icon string
	var color lipgloss.Color

	switch pr.State {
	case github.PRStateMerged:
		icon = "M"
		color = ColorPRMerged
	case github.PRStateClosed:
		icon = "X"
		color = ColorPRClosed
	case github.PRStateDraft:
		icon = "D"
		color = ColorPRDraft
	case github.PRStateOpen:
		// Check for merge conflicts first - this takes priority over check status
		if pr.Mergeable == "CONFLICTING" {
			icon = "C" // Conflicts
			color = ColorPRFailing
		} else {
			switch pr.CheckState {
			case github.CheckStatePassing:
				if pr.Mergeable == "MERGEABLE" {
					icon = "R" // Ready to merge
					color = ColorPROpen
				} else {
					icon = "P" // Passing
					color = ColorPROpen
				}
			case github.CheckStateFailing:
				icon = "F" // Failing
				color = ColorPRFailing
			case github.CheckStatePending:
				icon = "W" // Waiting/running
				color = ColorPRPending
			default:
				icon = "O" // Open, no checks
				color = ColorPROpen
			}
		}
	}

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(color).
		Padding(0, 0).
		Bold(true)

	return style.Render(icon)
}

// PRStatusIcon returns just the icon character for a PR status (without styling).
// This is useful for creating consistently-sized badges with different color schemes.
func PRStatusIcon(pr *github.PRInfo) string {
	if pr == nil {
		return ""
	}

	switch pr.State {
	case github.PRStateMerged:
		return "M"
	case github.PRStateClosed:
		return "X"
	case github.PRStateDraft:
		return "D"
	case github.PRStateOpen:
		// Check for merge conflicts first - this takes priority over check status
		if pr.Mergeable == "CONFLICTING" {
			return "C" // Conflicts
		}
		switch pr.CheckState {
		case github.CheckStatePassing:
			if pr.Mergeable == "MERGEABLE" {
				return "R" // Ready to merge
			}
			return "P" // Passing
		case github.CheckStateFailing:
			return "F" // Failing
		case github.CheckStatePending:
			return "W" // Waiting/running
		default:
			return "O" // Open, no checks
		}
	}
	return ""
}

// PRStatusDescription returns a human-readable description of PR status.
func PRStatusDescription(pr *github.PRInfo) string {
	if pr == nil {
		return ""
	}
	return pr.StatusDescription()
}

// Diff stat colors (git-style green/red)
var (
	ColorDiffAdd = lipgloss.Color("#98C379") // Green for additions
	ColorDiffDel = lipgloss.Color("#E06C75") // Red for deletions
)

// PRDiffStats returns a formatted diff stats string like "+123 -45" with colors.
// Returns empty string if no changes or PR is nil.
func PRDiffStats(pr *github.PRInfo) string {
	if pr == nil {
		return ""
	}
	if pr.Additions == 0 && pr.Deletions == 0 {
		return ""
	}

	addStyle := lipgloss.NewStyle().Foreground(ColorDiffAdd)
	delStyle := lipgloss.NewStyle().Foreground(ColorDiffDel)

	var parts []string
	if pr.Additions > 0 {
		parts = append(parts, addStyle.Render(formatDiffNum(pr.Additions, "+")))
	}
	if pr.Deletions > 0 {
		parts = append(parts, delStyle.Render(formatDiffNum(pr.Deletions, "-")))
	}

	return strings.Join(parts, " ")
}

// PRDiffStatsPlain returns diff stats without color (for selected items).
func PRDiffStatsPlain(pr *github.PRInfo) string {
	if pr == nil {
		return ""
	}
	if pr.Additions == 0 && pr.Deletions == 0 {
		return ""
	}

	var parts []string
	if pr.Additions > 0 {
		parts = append(parts, formatDiffNum(pr.Additions, "+"))
	}
	if pr.Deletions > 0 {
		parts = append(parts, formatDiffNum(pr.Deletions, "-"))
	}

	return strings.Join(parts, " ")
}

// formatDiffNum formats a number with prefix, using K suffix for large numbers.
func formatDiffNum(n int, prefix string) string {
	if n >= 10000 {
		return prefix + fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	if n >= 1000 {
		return prefix + fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return prefix + fmt.Sprintf("%d", n)
}
