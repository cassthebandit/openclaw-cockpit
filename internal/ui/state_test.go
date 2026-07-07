// File state_test.go validates tab and collapse state helpers.
package ui

import (
	"strings"
	"testing"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

// TestTabTitles ensures detail tabs appear alongside the overview tab.
func TestTabTitles(t *testing.T) {
	t.Parallel()

	m := &Model{}
	titles := m.tabTitles()
	if len(titles) != 1 || titles[0] != "Pulse" {
		t.Fatalf("tabTitles() = %v, want [Pulse]", titles)
	}

	m.sessions = []tmux.Session{{ID: "$1", Name: "dev"}}
	titles = m.tabTitles()
	if len(titles) != 2 {
		t.Fatalf("tabTitles() length = %d, want 2", len(titles))
	}
	if titles[1] != "dev" {
		t.Fatalf("session tab title = %q, want dev", titles[1])
	}
}

// TestRenderTabBarWrapsWhenNarrow ensures the tab bar flows into multiple lines when constrained.
func TestRenderTabBarWrapsWhenNarrow(t *testing.T) {
	t.Parallel()

	m := &Model{
		sessions: []tmux.Session{
			{ID: "s1", Name: "alpha"},
			{ID: "s2", Name: "beta"},
			{ID: "s3", Name: "gamma"},
			{ID: "s4", Name: "delta"},
		},
		zonePrefix: "component",
	}
	rendered := m.renderTabBar(12)
	if strings.Count(rendered, "\n") < 1 {
		t.Fatalf("expected multiline tab bar, got %q", rendered)
	}
}

// TestSetActiveTabUpdatesViewMode confirms view mode tracks the active tab.
func TestSetActiveTabUpdatesViewMode(t *testing.T) {
	t.Parallel()

	m := &Model{
		sessions: []tmux.Session{{ID: "s1", Name: "dev"}},
	}
	m.tabTitles()
	m.setActiveTab(0)
	if m.viewMode != viewModeOverview {
		t.Fatalf("viewMode = %v, want overview", m.viewMode)
	}

	m.setActiveTab(1)
	if m.viewMode != viewModeDetail {
		t.Fatalf("setActiveTab(1) should switch to detail, got %v", m.viewMode)
	}

	m.setActiveTab(0)
	if m.viewMode != viewModeOverview {
		t.Fatalf("setActiveTab(0) should return to overview, got %v", m.viewMode)
	}
}

// TestToggleCollapsed ensures session collapse state toggles predictably.
func TestToggleCollapsed(t *testing.T) {
	t.Parallel()

	m := &Model{collapsed: make(map[string]struct{})}
	if m.isCollapsed("s1") {
		t.Fatal("expected s1 to be expanded")
	}
	m.toggleCollapsed("s1")
	if !m.isCollapsed("s1") {
		t.Fatal("expected s1 to be collapsed")
	}
	m.toggleCollapsed("s1")
	if m.isCollapsed("s1") {
		t.Fatal("expected s1 to be expanded after toggle")
	}
}

// TestGroupCollapseDefaults verifies the seeded accordion defaults: Interactive
// Agents, Your Call, and System Problems expanded; everything else collapsed.
func TestGroupCollapseDefaults(t *testing.T) {
	t.Parallel()

	m := &Model{collapsedGroups: map[string]struct{}{}, seededGroups: map[string]struct{}{}}
	m.seedGroupCollapse([]cockpitGroup{
		groupInteractiveAgents, groupYourCall, groupSystemProblems,
		groupRuntime, groupDoneHeld, groupServices,
	})

	for _, g := range []cockpitGroup{groupInteractiveAgents, groupYourCall, groupSystemProblems} {
		if m.isGroupCollapsed(g.name) {
			t.Fatalf("group %q should default expanded", g.name)
		}
	}
	for _, g := range []cockpitGroup{groupRuntime, groupDoneHeld, groupServices} {
		if !m.isGroupCollapsed(g.name) {
			t.Fatalf("group %q should default collapsed", g.name)
		}
	}
}

// TestGroupCollapseTogglePersistsAcrossSeeding ensures user toggles survive
// later re-seeding (defaults apply once per group name).
func TestGroupCollapseTogglePersistsAcrossSeeding(t *testing.T) {
	t.Parallel()

	m := &Model{collapsedGroups: map[string]struct{}{}, seededGroups: map[string]struct{}{}}
	groups := []cockpitGroup{groupInteractiveAgents, groupRuntime}
	m.seedGroupCollapse(groups)

	m.toggleGroupCollapsed(groupInteractiveAgents.name) // expanded -> collapsed
	m.toggleGroupCollapsed(groupRuntime.name)           // collapsed -> expanded

	// A later render re-seeds; user choices must persist.
	m.seedGroupCollapse(groups)

	if !m.isGroupCollapsed(groupInteractiveAgents.name) {
		t.Fatal("user-collapsed Interactive Agents should stay collapsed after re-seed")
	}
	if m.isGroupCollapsed(groupRuntime.name) {
		t.Fatal("user-expanded Runtime should stay expanded after re-seed")
	}
}

// TestSetAllGroupsCollapsedToggle verifies collapse-all / expand-all.
func TestSetAllGroupsCollapsedToggle(t *testing.T) {
	t.Parallel()

	m := &Model{collapsedGroups: map[string]struct{}{}, seededGroups: map[string]struct{}{}}
	groups := []cockpitGroup{groupInteractiveAgents, groupYourCall, groupRuntime}

	m.setAllGroupsCollapsed(groups, true)
	if m.anyGroupExpanded(groups) {
		t.Fatal("collapse-all should leave no group expanded")
	}
	m.setAllGroupsCollapsed(groups, false)
	for _, g := range groups {
		if m.isGroupCollapsed(g.name) {
			t.Fatalf("expand-all should expand %q", g.name)
		}
	}
}
