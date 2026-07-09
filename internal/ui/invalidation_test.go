package ui

import (
	"testing"
	"time"
)

// classify populates the memo entries for every session so invalidation scope
// can be observed.
func classifyAll(m *Model) {
	for _, session := range m.sessions {
		_ = cockpitGroupFor(m, session)
		_ = sessionAttentionState(m, session)
	}
}

// TestPaneContentChangeInvalidatesOnlyThatSession is the F6 scope proof: a
// changed capture whose stale set is unaffected drops exactly the delivering
// session's memoized classification and keeps every other entry.
func TestPaneContentChangeInvalidatesOnlyThatSession(t *testing.T) {
	m, _ := frozenWallModel(t, 12, 4)
	_ = m.View()
	classifyAll(m)

	target := m.sessions[0]
	paneID := target.Windows[0].Panes[0].ID
	if _, ok := m.classifyCache[target.ID]; !ok {
		t.Fatalf("expected memoized classification for %s", target.ID)
	}
	other := m.sessions[1].ID
	otherEntry, ok := m.classifyCache[other]
	if !ok {
		t.Fatalf("expected memoized classification for %s", other)
	}

	m.Update(paneContentMsg{sessionID: target.ID, paneID: paneID, text: "new streaming line\n"})

	if _, ok := m.classifyCache[target.ID]; ok {
		t.Fatalf("target session classification should be invalidated")
	}
	if got, ok := m.classifyCache[other]; !ok || got != otherEntry {
		t.Fatalf("unrelated session classification should survive per-session invalidation")
	}
	if !m.renderDirty {
		t.Fatal("per-session invalidation must dirty the frame")
	}
}

// TestStaleSetChangeInvalidatesGlobally: when a capture flips the stale set,
// classification is a cross-session function and the whole memo must drop.
func TestStaleSetChangeInvalidatesGlobally(t *testing.T) {
	m, clock := frozenWallModel(t, 3, 0)
	_ = m.View()

	// Make the target's activity old enough that its next stale sweep marks
	// it (detached, live panes, no runtime): the capture below updates
	// lastChanged, so anchor the session and preview well past the threshold.
	target := m.sessions[3]
	paneID := target.Windows[0].Panes[0].ID
	m.sessions[3] = withActivity(target, clock.Add(-2*time.Hour))
	preview := m.previews[target.ID]
	preview.lastChanged = clock.Add(-2 * time.Hour)
	if m.updateStaleSessions() {
		// Consume the transition so the capture path observes its own change.
		m.invalidateClassifications()
	}
	classifyAll(m)
	if _, ok := m.classifyCache[m.sessions[0].ID]; !ok {
		t.Fatal("expected memoized classifications before capture")
	}

	// The changed capture refreshes preview.lastChanged to now, un-staling
	// the target: the stale set changes, so everything must invalidate.
	m.Update(paneContentMsg{sessionID: target.ID, paneID: paneID, text: "service woke up\n"})
	if len(m.classifyCache) != 0 {
		t.Fatalf("stale-set change should clear the whole classification memo, %d entries left", len(m.classifyCache))
	}
}

// TestSnapshotInvalidatesGlobally: snapshot swaps replace cross-session
// inputs wholesale and must keep the global invalidation path.
func TestSnapshotInvalidatesGlobally(t *testing.T) {
	m, _ := frozenWallModel(t, 3, 0)
	_ = m.View()
	classifyAll(m)
	if len(m.classifyCache) == 0 {
		t.Fatal("expected memoized classifications")
	}

	m.updateStaleSessions()
	m.invalidateClassifications()
	if len(m.classifyCache) != 0 {
		t.Fatal("global invalidation left memo entries behind")
	}
}

// TestUpdateStaleSessionsReportsChanges covers the change detector both ways.
func TestUpdateStaleSessionsReportsChanges(t *testing.T) {
	m, clock := frozenWallModel(t, 3, 0)
	_ = m.View()
	if m.updateStaleSessions() {
		t.Fatal("second sweep with unchanged inputs should report no change")
	}

	m.sessions[3] = withActivity(m.sessions[3], clock.Add(-2*time.Hour))
	m.previews[m.sessions[3].ID].lastChanged = clock.Add(-2 * time.Hour)
	if !m.updateStaleSessions() {
		t.Fatal("newly stale session should report a change")
	}
	if m.updateStaleSessions() {
		t.Fatal("repeat sweep should be stable")
	}
}

// TestTabTitlesOrganizedMatchesPreHoistBehavior locks the F7 hoist: organized
// overview keeps exactly the Pulse tab, organized detail appends the detail
// session, and non-organized mode still lists every filtered session.
func TestTabTitlesOrganizedMatchesPreHoistBehavior(t *testing.T) {
	m, _ := frozenWallModel(t, 3, 0)

	titles := m.tabTitles()
	if len(titles) != 1 || titles[0] != "Pulse" {
		t.Fatalf("organized overview tabTitles = %v, want [Pulse]", titles)
	}
	if len(m.tabSessionIDs) != 0 {
		t.Fatalf("organized overview should track no tab sessions, got %v", m.tabSessionIDs)
	}

	m.enterDetail(m.sessions[0].ID)
	titles = m.tabTitles()
	if len(titles) != 2 || titles[1] != m.sessions[0].Name {
		t.Fatalf("organized detail tabTitles = %v, want [Pulse %s]", titles, m.sessions[0].Name)
	}
	if len(m.tabSessionIDs) != 1 || m.tabSessionIDs[0] != m.sessions[0].ID {
		t.Fatalf("organized detail tabSessionIDs = %v", m.tabSessionIDs)
	}

	m.leaveDetail(true)
	m.organized = false
	titles = m.tabTitles()
	if len(titles) != len(m.filteredSessionsFull())+1 {
		t.Fatalf("non-organized tabTitles length = %d, want %d", len(titles), len(m.filteredSessionsFull())+1)
	}
}
