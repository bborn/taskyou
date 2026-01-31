// Package ui provides the terminal user interface.
package ui

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

//go:embed logo.png
var logoImageData []byte

// LogoStyle holds the styles for rendering the Tasky logo.
type LogoStyle struct {
	Lightning lipgloss.Style
	Cup       lipgloss.Style
}

// DefaultLogoStyle returns the default logo styling using theme colors.
func DefaultLogoStyle() LogoStyle {
	return LogoStyle{
		Lightning: lipgloss.NewStyle().Foreground(ColorWarning),    // Yellow
		Cup:       lipgloss.NewStyle().Foreground(ColorInProgress), // Orange
	}
}

// TerminalImageSupport represents the type of inline image support available.
type TerminalImageSupport int

const (
	ImageSupportNone TerminalImageSupport = iota
	ImageSupportITerm2
	ImageSupportKitty
)

// DetectImageSupport checks if the terminal supports inline images.
func DetectImageSupport() TerminalImageSupport {
	// iTerm2
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" || os.Getenv("LC_TERMINAL") == "iTerm2" {
		return ImageSupportITerm2
	}

	// Kitty
	if strings.Contains(os.Getenv("TERM"), "kitty") || os.Getenv("KITTY_WINDOW_ID") != "" {
		return ImageSupportKitty
	}

	// WezTerm supports iTerm2 protocol
	if os.Getenv("TERM_PROGRAM") == "WezTerm" {
		return ImageSupportITerm2
	}

	return ImageSupportNone
}

// RenderInlineImage renders an image using terminal escape sequences.
// Returns empty string if not supported or no image data.
func RenderInlineImage(imageData []byte, widthCells, heightCells int) string {
	if len(imageData) == 0 {
		return ""
	}

	support := DetectImageSupport()
	b64Data := base64.StdEncoding.EncodeToString(imageData)

	switch support {
	case ImageSupportITerm2:
		// iTerm2 inline image protocol: OSC 1337 ; File=[args] : base64 ST
		return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\x07",
			widthCells, heightCells, b64Data)

	case ImageSupportKitty:
		// Kitty graphics protocol - transmit and display
		return fmt.Sprintf("\x1b_Ga=T,f=100,c=%d,r=%d;%s\x1b\\",
			widthCells, heightCells, b64Data)

	default:
		return ""
	}
}

// RenderTaskyHeader renders the Tasky logo for the dashboard header.
// Shows inline image if terminal supports it, otherwise falls back to emoji.
func RenderTaskyHeader(width int) string {
	if width < 20 {
		return ""
	}

	var logo string

	// Try inline image first (2 cells wide, 1 cell tall)
	if len(logoImageData) > 0 {
		if img := RenderInlineImage(logoImageData, 2, 1); img != "" {
			logo = img
		}
	}

	// Fallback to emoji
	if logo == "" {
		style := DefaultLogoStyle()
		logo = style.Lightning.Render("⚡") + style.Cup.Render("☕")
	}

	brandStyle := lipgloss.NewStyle().
		Foreground(ColorInProgress).
		Bold(true)
	brand := brandStyle.Render(" TaskYou")

	header := logo + brand

	// Right-align
	headerStyle := lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Right)

	return headerStyle.Render(header)
}

// RenderTaskyCompact renders the compact emoji-only logo.
func RenderTaskyCompact() string {
	style := DefaultLogoStyle()
	return style.Lightning.Render("⚡") + style.Cup.Render("☕")
}

// RenderTaskyInline is an alias for RenderTaskyCompact.
func RenderTaskyInline() string {
	return RenderTaskyCompact()
}

// RenderTaskyFull renders the logo with brand name.
func RenderTaskyFull() string {
	logo := RenderTaskyCompact()

	brandStyle := lipgloss.NewStyle().
		Foreground(ColorInProgress).
		Bold(true)

	return logo + brandStyle.Render(" TaskYou")
}

// RenderTaskyMedium renders the medium logo.
func RenderTaskyMedium() string {
	return RenderTaskyFull()
}

// RenderTaskyWithTagline renders the logo with tagline.
func RenderTaskyWithTagline() string {
	logo := RenderTaskyFull()

	taglineStyle := lipgloss.NewStyle().
		Foreground(ColorMuted).
		Italic(true)

	return logo + taglineStyle.Render(" — calm, cool, supercharged")
}

// TaskyHeaderHeight returns the height of the Tasky header (always 1 line).
func TaskyHeaderHeight() int {
	return 1
}

// Constants
const (
	TaskyASCII      = "⚡☕"
	TaskyASCIISmall = "⚡☕"
)
