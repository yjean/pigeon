package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCtrlEnterSends(t *testing.T) {
	if got := (tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}).String(); got != "ctrl+enter" {
		t.Fatalf("ctrl+enter reported as %q", got)
	}
}
