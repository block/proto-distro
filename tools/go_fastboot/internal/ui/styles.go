// Package ui provides terminal styling and visual components.
// It uses lipgloss for colors and formatting, and implements spinners and status displays.
//
// This file defines color schemes, styles, and formatting utilities.
package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// Colors
var (
	ColorSuccess = lipgloss.Color("#00ff00")
	ColorWarning = lipgloss.Color("#ffff00")
	ColorError   = lipgloss.Color("#ff0000")
	ColorInfo    = lipgloss.Color("#00ffff")
	ColorMuted   = lipgloss.Color("#666666")
)

// Styles
var (
	StyleSuccess = lipgloss.NewStyle().Foreground(ColorSuccess)
	StyleWarning = lipgloss.NewStyle().Foreground(ColorWarning)
	StyleError   = lipgloss.NewStyle().Foreground(ColorError)
	StyleInfo    = lipgloss.NewStyle().Foreground(ColorInfo)
	StyleMuted   = lipgloss.NewStyle().Foreground(ColorMuted)
	StyleBold    = lipgloss.NewStyle().Bold(true)
)

// Icons for status messages (colored)
var (
	SuccessIcon = StyleSuccess.Render("✓")
	ErrorIcon   = StyleError.Render("✗")
	WarningIcon = StyleWarning.Render("!")
	InfoIcon    = StyleInfo.Render("→")
)

// StyleMode formats a mode string for display with appropriate color
func StyleMode(mode string) string {
	switch mode {
	case "LINUX":
		return StyleSuccess.Render("[LINUX]")
	case "FASTBOOT":
		return StyleWarning.Render("[FASTBOOT]")
	case "UBOOT":
		return StyleInfo.Render("[UBOOT]")
	default:
		return StyleMuted.Render("[UNKNOWN]")
	}
}

// FormatIP formats an IP address for display
func FormatIP(ip string, reachable bool) string {
	if reachable {
		return StyleSuccess.Render(ip)
	}
	return StyleError.Render(ip)
}
