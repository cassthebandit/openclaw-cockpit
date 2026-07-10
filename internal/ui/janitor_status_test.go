package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestLoadJanitorStatusFileStates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 20, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	missing := loadJanitorStatusFile(filepath.Join(dir, "missing.json"), now)
	if missing.State != "missing" {
		t.Fatalf("missing state = %q", missing.State)
	}

	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(`{"status_version":1,"generated_at":"2026-07-07T19:59:00Z","sessions":{},"last_cycle":{"mark":1,"refuse":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := loadJanitorStatusFile(path, now)
	if ok.State != "ok" || ok.Cycle.Mark != 1 || ok.Cycle.Refuse != 2 {
		t.Fatalf("ok status = %#v", ok)
	}

	if err := os.WriteFile(path, []byte(`{"status_version":1,"generated_at":"2026-07-07T19:50:00Z","sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := loadJanitorStatusFile(path, now)
	if stale.State != "stale" {
		t.Fatalf("stale state = %q", stale.State)
	}

	if err := os.WriteFile(path, []byte(`{"status_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := loadJanitorStatusFile(path, now)
	if invalid.State != "invalid" {
		t.Fatalf("invalid state = %q", invalid.State)
	}
}

func TestJanitorStatusFooterLine(t *testing.T) {
	t.Parallel()

	m := &Model{janitorStatus: janitorStatusView{State: "ok", Cycle: janitorCycleStatus{Mark: 1, Refuse: 2}}}
	line := m.janitorStatusLine(120)
	for _, want := range []string{"janitor: ok", "marked 1", "held/refused 2"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line missing %q in %q", want, line)
		}
	}
}

func TestJanitorStatusFooterLineTruncatesDisplayWidthSafe(t *testing.T) {
	t.Parallel()

	// Multibyte detail must never be byte-sliced into a broken rune.
	m := &Model{janitorStatus: janitorStatusView{
		State:  "stale",
		Detail: "статус застарів на 45 хвилин · 状態が古い",
		Cycle:  janitorCycleStatus{Mark: 3, Refuse: 2},
	}}
	for _, width := range []int{10, 20, 30, 40} {
		line := m.janitorStatusLine(width)
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("width %d: rendered janitor line display width = %d, line %q", width, got, line)
		}
		if strings.ContainsRune(line, '�') {
			t.Fatalf("width %d: janitor line contains replacement char: %q", width, line)
		}
	}
}

func TestManualCollapsePersistsAcrossJanitorAndSnapshotRefresh(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	writeStatus := func(generated time.Time) {
		payload := `{"status_version":1,"generated_at":"` + generated.UTC().Format("2006-01-02T15:04:05Z") +
			`","sessions":{"held-lane":{"janitor_state":"protected"}},"last_cycle":{"refuse":1}}`
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus(time.Now())

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.SetJanitorStatusFile(path)
	m.seedGroupCollapse(primaryCockpitGroups())

	m.toggleGroupCollapsed(groupActiveAgents.name)
	if !m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatalf("toggle did not collapse Active Agents")
	}

	// Janitor sidecar reload (new generation) must not reopen the group.
	writeStatus(time.Now().Add(time.Second))
	m.refreshJanitorStatus()
	if !m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatalf("janitor status reload reopened a manually collapsed group")
	}

	// Snapshot swap + reseeding on the next frame must not reopen it either.
	m.sessions = []tmux.Session{sessionForGroup("fresh-agent", "claude", "/workspace", "")}
	m.invalidateClassifications()
	m.seedGroupCollapse(primaryCockpitGroups())
	if !m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatalf("snapshot refresh/reseeding reopened a manually collapsed group")
	}
}
