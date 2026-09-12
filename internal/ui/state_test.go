// File state_test.go validates tab and collapse state helpers.
package ui

import (
	"strings"
	"testing"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
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

// TestGroupCollapseDefaults verifies the seeded accordion defaults: the agent
// bands plus operator/runtime problem bands start expanded; everything else
// starts collapsed.
func TestGroupCollapseDefaults(t *testing.T) {
	t.Parallel()

	m := &Model{collapsedGroups: map[string]struct{}{}, seededGroups: map[string]struct{}{}}
	m.seedGroupCollapse([]cockpitGroup{
		groupActiveAgents, groupMarkedForTeardown, groupFailedAgents, groupOperationalFailures, groupSubsystemFailures,
		groupCompletedAgents, groupServices,
	})

	for _, g := range []cockpitGroup{groupActiveAgents, groupMarkedForTeardown, groupFailedAgents, groupOperationalFailures, groupSubsystemFailures} {
		if m.isGroupCollapsed(g.name) {
			t.Fatalf("group %q should default expanded", g.name)
		}
	}
	for _, g := range []cockpitGroup{groupCompletedAgents, groupServices} {
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
	groups := []cockpitGroup{groupActiveAgents, groupCompletedAgents}
	m.seedGroupCollapse(groups)

	m.toggleGroupCollapsed(groupActiveAgents.name)    // expanded -> collapsed
	m.toggleGroupCollapsed(groupCompletedAgents.name) // collapsed -> expanded

	// A later render re-seeds; user choices must persist.
	m.seedGroupCollapse(groups)

	if !m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatal("user-collapsed Active Agents should stay collapsed after re-seed")
	}
	if m.isGroupCollapsed(groupCompletedAgents.name) {
		t.Fatal("user-expanded Completed Agent Runs should stay expanded after re-seed")
	}
}

// TestSetAllGroupsCollapsedToggle verifies collapse-all / expand-all.
func TestSetAllGroupsCollapsedToggle(t *testing.T) {
	t.Parallel()

	m := &Model{collapsedGroups: map[string]struct{}{}, seededGroups: map[string]struct{}{}}
	groups := []cockpitGroup{groupActiveAgents, groupOperationalFailures, groupSubsystemFailures}

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
