package themepicker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tonhe/headsup/internal/icons"
	"github.com/tonhe/headsup/internal/theme"
	"github.com/tonhe/headsup/internal/ui/uitest"
)

func runes(s string) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func msgOf(c tea.Cmd) tea.Msg {
	if c == nil {
		return nil
	}
	return c()
}

func picker(profile string) *Model {
	m := New(uitest.StyleFor("tokyo-night"), icons.Unicode, theme.All(), "tokyo-night", profile)
	m.SetSize(86, 24)
	return m
}

func TestSearchPreviewSelect(t *testing.T) {
	m := picker("")
	if m.Current() != "tokyo-night" || m.Orig() != "tokyo-night" {
		t.Fatalf("cursor should start on the active theme, got %s", m.Current())
	}
	var prev tea.Msg
	for _, r := range "dracula" {
		if msg := msgOf(m.Update(runes(string(r)))); msg != nil {
			prev = msg
		}
	}
	if p, ok := prev.(PreviewMsg); !ok || p.Slug != "dracula" {
		t.Fatalf("typing should preview dracula, got %#v", prev)
	}
	if s, ok := msgOf(m.Update(key(tea.KeyEnter))).(SelectedMsg); !ok || s.Slug != "dracula" || s.ProfileOnly {
		t.Fatalf("enter = %#v", s)
	}
	if msgOf(m.Update(key(tea.KeyCtrlT))) != nil {
		t.Error("ctrl+t without a profile should do nothing")
	}
	m.Update(key(tea.KeyBackspace))
	m.Update(key(tea.KeyCtrlU))
	m.Update(runes("zzzqqq"))
	if m.Current() != "" || msgOf(m.Update(key(tea.KeyEnter))) != nil {
		t.Error("no match: nothing to select")
	}
	if !strings.Contains(ansi.Strip(m.View()), "No themes match") {
		t.Error("empty state missing")
	}
	m.Update(key(tea.KeyEsc)) // clears query
	if c, ok := msgOf(m.Update(key(tea.KeyEsc))).(CancelMsg); !ok || c.Orig != "tokyo-night" {
		t.Errorf("esc = %#v", c)
	}
}

func TestProfileOnlyAndReload(t *testing.T) {
	m := picker("work")
	m.Update(key(tea.KeyDown))
	if s, ok := msgOf(m.Update(key(tea.KeyCtrlT))).(SelectedMsg); !ok || !s.ProfileOnly {
		t.Errorf("ctrl+t = %#v", s)
	}
	if _, ok := msgOf(m.Update(key(tea.KeyCtrlR))).(ReloadMsg); !ok {
		t.Error("ctrl+r should ask for a reload")
	}
	cur := m.Current()
	extra := theme.Theme{Name: "Zed Extra", Slug: "zed-extra", Source: "/tmp/x.toml", Light: true,
		Base00: theme.Current.Base00, Base05: theme.Current.Base05}
	m.SetThemes(append(theme.All(), extra))
	if m.Current() != cur {
		t.Error("reload should keep the cursor")
	}
	m.Update(runes("zed extra"))
	if m.Current() != "zed-extra" {
		t.Fatalf("new theme not listed: %v", m.Shown())
	}
	if !strings.Contains(ansi.Strip(m.View()), "user") {
		t.Error("user themes should be tagged")
	}
	if !strings.Contains(ansi.Strip(m.View()), "this profile only") {
		t.Error("profile hint expected")
	}
}

func TestFilterMovementView(t *testing.T) {
	m := picker("")
	m.Update(key(tea.KeyTab))
	if m.Filter() != FilterDark {
		t.Fatal("tab -> dark")
	}
	for _, s := range m.Shown() {
		if theme.ByName(s).Light {
			t.Fatalf("dark filter shows %s", s)
		}
	}
	m.Update(key(tea.KeyTab))
	for _, s := range m.Shown() {
		if !theme.ByName(s).Light {
			t.Fatalf("light filter shows %s", s)
		}
	}
	m.Update(key(tea.KeyShiftTab))
	m.Update(key(tea.KeyShiftTab))
	if m.Filter() != FilterAll || FilterLight.String() != "light" || FilterAll.String() != "all" {
		t.Error("shift+tab cycles back")
	}
	for _, k := range []tea.KeyType{tea.KeyEnd, tea.KeyPgUp, tea.KeyHome, tea.KeyPgDown, tea.KeyUp, tea.KeyCtrlN, tea.KeyCtrlP, tea.KeyF1} {
		m.Update(key(k))
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft})
	m.Update(key(tea.KeySpace))
	m.Update(nil)
	for _, sz := range [][2]int{{86, 24}, {50, 12}, {76, 20}} {
		m.SetSize(sz[0], sz[1])
		lines := strings.Split(m.View(), "\n")
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != sz[0] {
				t.Errorf("%v line %d width %d", sz, i, lipgloss.Width(l))
			}
		}
	}
	m.SetSize(10, 4)
	if m.View() != "" {
		t.Error("too small renders nothing")
	}
	if _, ok := msgOf(m.Update(key(tea.KeyCtrlC))).(CancelMsg); !ok {
		t.Error("ctrl+c cancels")
	}
}
