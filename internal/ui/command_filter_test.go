package ui

import (
	"testing"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

func TestApplyViewFilterCommandNarrowsSessions(t *testing.T) {
	t.Parallel()

	route := sessionForGroup("runtime-route", "openclaw-runtime", "OpenClaw Runtime", "route")
	route.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
	decision := sessionForGroup("runtime-decision", "openclaw-runtime", "OpenClaw Runtime", "decision")
	decision.Windows[0].Panes[0].Cockpit = testRuntimeMeta("needs_decision")
	service := sessionForGroup("smonitor", "go2rtc", "/workspace/config/smonitor", "smonitor")

	m := &Model{sessions: []tmux.Session{route, decision, service}}
	if !m.applyViewFilterCommand("route") {
		t.Fatalf("route command rejected")
	}
	got := m.filteredSessionsFull()
	if len(got) != 1 || got[0].ID != route.ID {
		t.Fatalf(":route filtered sessions = %#v, want only route", got)
	}

	if !m.applyViewFilterCommand("services") {
		t.Fatalf("services command rejected")
	}
	got = m.filteredSessionsFull()
	if len(got) != 1 || got[0].ID != service.ID {
		t.Fatalf(":services filtered sessions = %#v, want only service", got)
	}

	m.searchQuery = "decision"
	if !m.applyViewFilterCommand("all") {
		t.Fatalf("all command rejected")
	}
	if m.viewFilter != "" {
		t.Fatalf(":all should clear view filter, got %q", m.viewFilter)
	}
	if m.searchQuery != "decision" {
		t.Fatalf(":all should not clear / search, got %q", m.searchQuery)
	}
	got = m.filteredSessionsFull()
	if len(got) != 1 || got[0].ID != decision.ID {
		t.Fatalf("/ search should remain active after :all, got %#v", got)
	}
}

func TestApplyViewFilterCommandRejectsUnknown(t *testing.T) {
	t.Parallel()

	m := &Model{viewFilter: "route"}
	if m.applyViewFilterCommand("bogus") {
		t.Fatalf("bogus command should be rejected")
	}
	if m.viewFilter != "route" {
		t.Fatalf("unknown command changed filter to %q", m.viewFilter)
	}
}

func testRuntimeMeta(presentationGroup string) *tmux.CockpitMeta {
	return &tmux.CockpitMeta{
		ManagedBy:         "openclaw_runtime_snapshot",
		Kind:              "runtime",
		Agent:             "openclaw-runtime",
		State:             "review",
		DisplayGroup:      "needs_attention",
		PresentationGroup: presentationGroup,
	}
}
