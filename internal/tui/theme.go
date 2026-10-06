package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Catppuccin Mocha, https://catppuccin.com/palette. Backgrounds are never
// painted (except selection and badges) so a transparent terminal stays so.
var (
	rosewater = lipgloss.Color("#f5e0dc")
	mauve     = lipgloss.Color("#cba6f7")
	red       = lipgloss.Color("#f38ba8")
	peach     = lipgloss.Color("#fab387")
	yellow    = lipgloss.Color("#f9e2af")
	green     = lipgloss.Color("#a6e3a1")
	blue      = lipgloss.Color("#89b4fa")
	lavender  = lipgloss.Color("#b4befe")
	text      = lipgloss.Color("#cdd6f4")
	subtext0  = lipgloss.Color("#a6adc8")
	overlay1  = lipgloss.Color("#7f849c")
	overlay0  = lipgloss.Color("#6c7086")
	surface1  = lipgloss.Color("#45475a")
	surface0  = lipgloss.Color("#313244")
	crust     = lipgloss.Color("#11111b")
)

// Roles, following the Catppuccin style guide and LazyVim's lualine modes.
var (
	cBorder       = surface1 // inactive pane border, rules
	cBorderActive = lavender // focused pane border
	cSelBg        = surface0 // selected row (cursorline)
	cSelBar       = lavender
	cUnread       = blue
	cCount        = peach // unread counts
	cStar         = yellow
	cOK           = green
	cWarn         = yellow
	cBusy         = peach
	cErr          = red
	cInsert       = green // compose: INSERT mode, like nvim
	cCursor       = rosewater

	sBase   = lipgloss.NewStyle().Foreground(text)
	sBold   = lipgloss.NewStyle().Foreground(text).Bold(true)
	sMuted  = lipgloss.NewStyle().Foreground(subtext0)
	sDim    = lipgloss.NewStyle().Foreground(overlay1)
	sFaint  = lipgloss.NewStyle().Foreground(overlay0)
	sRule   = lipgloss.NewStyle().Foreground(surface1)
	sDate   = lipgloss.NewStyle().Foreground(overlay1).Italic(true)
	sTitle  = lipgloss.NewStyle().Foreground(mauve).Bold(true)
	sName   = lipgloss.NewStyle().Foreground(blue).Bold(true)
	sKey    = lipgloss.NewStyle().Foreground(blue)
	sNormal = lipgloss.NewStyle().Foreground(crust).Background(blue).Bold(true)
	sRead   = lipgloss.NewStyle().Foreground(crust).Background(mauve).Bold(true)
	sInsert = lipgloss.NewStyle().Foreground(crust).Background(green).Bold(true)
	sBadge  = lipgloss.NewStyle().Foreground(crust).Background(lavender).Bold(true)
)

func fg(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c)
}
