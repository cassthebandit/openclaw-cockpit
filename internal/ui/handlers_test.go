// File handlers_test.go validates keyboard and mouse handlers used by the UI.
package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// TestTmuxKeysFrom ensures Bubble Tea key messages map to tmux key strings.
func TestTmuxKeysFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		msg    tea.KeyMsg
		want   []string
		expect bool
	}{
		{name: "enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter}, want: []string{"Enter"}, expect: true},
		{name: "space", msg: tea.KeyPressMsg{Code: tea.KeySpace}, want: []string{" "}, expect: true},
		{name: "alt runes rejected", msg: tea.KeyPressMsg{Text: "a", Code: 'a', Mod: tea.ModAlt}, expect: false},
		{name: "runes ok", msg: tea.KeyPressMsg{Text: "a", Code: 'a'}, want: []string{"a"}, expect: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tmuxKeysFrom(tt.msg)
			if ok != tt.expect {
				t.Fatalf("tmuxKeysFrom ok = %v, want %v", ok, tt.expect)
			}
			if !tt.expect {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("tmuxKeysFrom len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("tmuxKeysFrom got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestTabIndexFromZoneIDs checks zone identifiers convert to tab indexes.
func TestTabIndexFromZoneIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ids      []string
		wantIdx  int
		expectOK bool
	}{
		{name: "no match", ids: []string{"foo"}},
		{name: "root id", ids: []string{"component###tab:0"}, wantIdx: 0, expectOK: true},
		{name: "multiple ids", ids: []string{"component###tab:1", "other"}, wantIdx: 1, expectOK: true},
		{name: "parse failure", ids: []string{"component###tab:notint"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx, ok := tabIndexFromZoneIDs(tt.ids)
			if ok != tt.expectOK {
				t.Fatalf("tabIndexFromZoneIDs ok = %v, want %v", ok, tt.expectOK)
			}
			if !ok {
				return
			}
			if idx != tt.wantIdx {
				t.Fatalf("tabIndexFromZoneIDs = %d, want %d", idx, tt.wantIdx)
			}
		})
	}
}

// TestHandleTabMouseClick confirms tab clicks update the active tab state.
func TestHandleTabMouseClick(t *testing.T) {
	zone.NewGlobal()
	m := &Model{
		sessions:   []tmux.Session{{ID: "sess", Name: "sess"}},
		hidden:     make(map[string]struct{}),
		stale:      make(map[string]struct{}),
		collapsed:  make(map[string]struct{}),
		zonePrefix: zone.NewPrefix(),
		width:      80,
	}
	bar := m.renderTabBar(80)
	_ = zone.Scan(bar)
	tabID := m.zonePrefix + "###tab:1"
	var info *zone.ZoneInfo
	deadline := time.Now().Add(50 * time.Millisecond)
	for info == nil && time.Now().Before(deadline) {
		info = zone.Get(tabID)
		time.Sleep(time.Millisecond)
	}
	if info == nil {
		t.Fatal("expected session tab zone to be registered")
	}

	msg := tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft}
	ids := zone.DefaultManager.IDsInBounds(msg)
	if len(ids) == 0 {
		ids = zone.DefaultManager.IDsInBounds(tea.MouseMotionMsg{X: info.StartX, Y: info.StartY})
	}
	if len(ids) == 0 {
		t.Fatalf("expected zone ids at (%d,%d)", info.StartX, info.StartY)
	}
	handled, cmd := m.handleTabMouse(msg)
	if !handled {
		t.Fatal("expected tab click to be handled")
	}
	if cmd != nil {
		t.Fatal("expected no command from tab click")
	}
	if m.activeTab != 1 {
		t.Fatalf("activeTab = %d, want 1", m.activeTab)
	}
}

// TestHandleMouseHoverSetsState ensures motion tracking highlights the card.
func TestHandleMouseHoverSetsState(t *testing.T) {
	zone.DefaultManager = zone.New()
	m := &Model{
		previews: map[string]*sessionPreview{
			"s1": {viewport: func() *viewport.Model {
				vp := viewportFor(innerDimension{width: 60, height: 20})
				return &vp
			}()},
		},
		sessions: []tmux.Session{{
			ID: "s1",
			Windows: []tmux.Window{{
				Active: true,
				Panes:  []tmux.Pane{{ID: "%1", Active: true}},
			}},
		}},
		hidden:     make(map[string]struct{}),
		stale:      make(map[string]struct{}),
		collapsed:  make(map[string]struct{}),
		zonePrefix: zone.NewPrefix(),
		width:      120,
		height:     50,
	}

	view := m.renderSessionPreviews(0)
	_ = zone.Scan(view)

	card := m.cardLayout[0]
	var info *zone.ZoneInfo
	deadline := time.Now().Add(500 * time.Millisecond)
	for info == nil && time.Now().Before(deadline) {
		info = zone.Get(card.zoneID)
		time.Sleep(2 * time.Millisecond)
	}
	if info == nil {
		t.Fatal("expected zone info to be registered")
	}

	m.handleMouse(tea.MouseMotionMsg{X: info.StartX, Y: info.StartY})
	if m.hoveredSession != "s1" {
		t.Fatalf("hoveredSession = %q, want s1", m.hoveredSession)
	}
}

func TestRenderedCloseControlHidesLocally(t *testing.T) {
	zone.DefaultManager = zone.New()
	m := renderedRuntimeModel(t, "needs_decision", 5)

	view := m.renderSessionPreviews(0)
	_ = zone.Scan(view)
	if len(m.cardLayout) < 2 {
		t.Fatalf("cardLayout len = %d, want multiple rendered runtime cards", len(m.cardLayout))
	}
	card := m.cardLayout[1]
	info := waitForZone(t, card.closeZoneID)

	_, cmd := m.handleMouse(tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft})
	if _, ok := m.hidden[card.sessionID]; !ok {
		t.Fatalf("session %q was not locally hidden", card.sessionID)
	}
	if _, ok := m.previews[card.sessionID]; ok {
		t.Fatalf("preview for hidden session %q should be removed", card.sessionID)
	}
	if cmd == nil {
		t.Fatal("expected local hide status command")
	}
	got, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("hide command returned %#v, want statusMsg", cmd())
	}
	if !strings.Contains(string(got), "Hidden in Cockpit:") {
		t.Fatalf("hide status = %q, want local hide wording", got)
	}
	if strings.Contains(string(got), "Closed session") {
		t.Fatalf("hide status should not imply close/kill: %q", got)
	}
}

func TestRenderedCollapseControlFlipsToExpand(t *testing.T) {
	zone.DefaultManager = zone.New()
	m := renderedRuntimeModel(t, "route_health", 5)

	view := m.renderSessionPreviews(0)
	_ = zone.Scan(view)
	if len(m.cardLayout) == 0 {
		t.Fatal("expected rendered runtime cards")
	}
	card := m.cardLayout[0]
	info := waitForZone(t, card.collapseZoneID)

	_, cmd := m.handleMouse(tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft})
	if cmd != nil {
		t.Fatalf("collapse click returned unexpected command %#v", cmd)
	}
	if !m.isCollapsed(card.sessionID) {
		t.Fatalf("session %q was not collapsed", card.sessionID)
	}
	view = m.renderSessionPreviews(0)
	if !strings.Contains(view, expandLabel) {
		t.Fatalf("collapsed render missing expand control %q in %q", expandLabel, view)
	}
}

func TestCockpitCleanupKeysAreDisabled(t *testing.T) {
	t.Parallel()

	m := &Model{
		focusedSession: "s1",
		stale:          map[string]struct{}{"s1": {}},
	}

	handled, cmd := m.handleGlobalKey(tea.KeyPressMsg{Text: "X", Code: 'X'})
	if !handled {
		t.Fatal("expected X to be handled")
	}
	if cmd == nil {
		t.Fatal("expected status command for disabled cleanup")
	}
	if got, ok := cmd().(statusMsg); !ok || got == "" {
		t.Fatalf("expected statusMsg from disabled cleanup, got %#v", cmd())
	}

	handled, cmd = m.handleGlobalKey(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("expected ctrl+x to be handled")
	}
	if got, ok := cmd().(statusMsg); !ok || got == "" {
		t.Fatalf("expected statusMsg from disabled bulk cleanup, got %#v", cmd())
	}
}

func renderedRuntimeModel(t *testing.T, presentationGroup string, count int) *Model {
	t.Helper()
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 90
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardCols = 4
	m.cardInnerWidth = 86
	m.cardInnerHeight = 6
	for i := 0; i < count; i++ {
		session := sessionForGroup("runtime-"+presentationGroup+"-"+string(rune('a'+i)), "openclaw-runtime", "OpenClaw Runtime", presentationGroup)
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta(presentationGroup)
		m.sessions = append(m.sessions, session)
		vp := viewportFor(innerDimension{width: 86, height: 6})
		vp.SetContent("runtime card content")
		m.previews[session.ID] = &sessionPreview{viewport: &vp, paneID: session.Windows[0].Panes[0].ID}
	}
	// These helpers exercise rendered per-card controls, so keep every accordion
	// group expanded; default collapse behavior is covered by dedicated tests.
	m.setAllGroupsCollapsed(orderedCockpitGroups(m, m.filteredSessions()), false)
	return m
}

func waitForZone(t *testing.T, id string) *zone.ZoneInfo {
	t.Helper()
	var info *zone.ZoneInfo
	deadline := time.Now().Add(500 * time.Millisecond)
	for info == nil && time.Now().Before(deadline) {
		info = zone.Get(id)
		time.Sleep(2 * time.Millisecond)
	}
	if info == nil {
		t.Fatalf("expected zone %q to be registered", id)
	}
	return info
}

func TestDKeyEntersDetailForCursorSession(t *testing.T) {
	t.Parallel()

	m := &Model{
		cursorSession: "s1",
		sessions: []tmux.Session{{
			ID:   "s1",
			Name: "runtime grouped",
		}},
		previews: map[string]*sessionPreview{
			"s1": {viewport: &viewport.Model{}},
		},
	}

	handled, cmd := m.handleGlobalKey(tea.KeyPressMsg{Text: "d", Code: 'd'})
	if !handled || cmd != nil {
		t.Fatalf("d key handled=%v cmd=%v, want handled without cmd", handled, cmd)
	}
	if m.viewMode != viewModeDetail || m.detailSession != "s1" {
		t.Fatalf("d key did not enter detail: mode=%v detail=%q", m.viewMode, m.detailSession)
	}
}

func TestDKeyEntersDetailWhenOrganized(t *testing.T) {
	t.Parallel()

	m := &Model{
		organized:     true,
		cursorSession: "s1",
		sessions: []tmux.Session{{
			ID:   "s1",
			Name: "runtime grouped",
		}},
		previews: map[string]*sessionPreview{
			"s1": {viewport: &viewport.Model{}},
		},
	}

	handled, _ := m.handleGlobalKey(tea.KeyPressMsg{Text: "d", Code: 'd'})
	if !handled {
		t.Fatal("d key should be handled")
	}
	if m.viewMode != viewModeDetail || m.detailSession != "s1" || m.activeTab != 1 {
		t.Fatalf("organized d key did not enter detail: mode=%v detail=%q tab=%d", m.viewMode, m.detailSession, m.activeTab)
	}
}

func TestMonitorOnlyBlocksKeyForwarding(t *testing.T) {
	t.Parallel()

	vp := viewportFor(innerDimension{width: 60, height: 20})
	m := &Model{
		monitorOnly:    true,
		focusedSession: "s1",
		previews: map[string]*sessionPreview{
			"s1": {viewport: &vp, paneID: "%1"},
		},
		sessions: []tmux.Session{{
			ID: "s1",
			Windows: []tmux.Window{{
				Active: true,
				Panes:  []tmux.Pane{{ID: "%1", Active: true}},
			}},
		}},
	}

	handled, cmd := m.handleFocusedKey(tea.KeyPressMsg{Text: "a", Code: 'a'})
	if !handled {
		t.Fatal("expected key to be handled in monitor-only mode")
	}
	if cmd == nil {
		t.Fatal("expected status command for disabled key forwarding")
	}
	if got, ok := cmd().(statusMsg); !ok || got == "" {
		t.Fatalf("expected statusMsg from disabled key forwarding, got %#v", cmd())
	}
}

// TestControlUnderPointer identifies active control zones for hover styling.

// accordionModel builds an organized overview model with one live Interactive
// Agents card (expanded by default) and three Runtime cards (collapsed by
// default) for exercising accordion behavior.
func accordionModel(t *testing.T) *Model {
	t.Helper()
	zone.DefaultManager = zone.New()
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 200
	m.height = 60
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardCols = 4
	m.cardInnerWidth = 40
	m.cardInnerHeight = 6

	m.sessions = append(m.sessions, agentSessionForGroup("live-agent", "running"))
	for i := 0; i < 3; i++ {
		s := sessionForGroup("runtime-route-"+string(rune('a'+i)), "openclaw-runtime", "OpenClaw Runtime", "route")
		s.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
		m.sessions = append(m.sessions, s)
	}
	for _, s := range m.sessions {
		vp := viewportFor(innerDimension{width: 40, height: 6})
		vp.SetContent("card content for " + s.ID)
		m.previews[s.ID] = &sessionPreview{viewport: &vp, paneID: s.Windows[0].Panes[0].ID}
	}
	return m
}

func TestGroupDividerClickTogglesCollapse(t *testing.T) {
	m := accordionModel(t)
	view := m.renderSessionPreviews(0)
	_ = zone.Scan(view)

	if !m.isGroupCollapsed(groupRuntime.name) {
		t.Fatal("Runtime should start collapsed by default")
	}
	zoneID := ""
	for _, gz := range m.groupZones {
		if gz.name == groupRuntime.name {
			zoneID = gz.zoneID
		}
	}
	if zoneID == "" {
		t.Fatalf("no group zone recorded for %q; zones=%#v", groupRuntime.name, m.groupZones)
	}
	info := waitForZone(t, zoneID)

	m.handleMouse(tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft})
	if m.isGroupCollapsed(groupRuntime.name) {
		t.Fatal("divider click should expand a collapsed group")
	}
	m.handleMouse(tea.MouseClickMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseLeft})
	if !m.isGroupCollapsed(groupRuntime.name) {
		t.Fatal("second divider click should collapse the group again")
	}
}

func TestGroupCollapseKeyTogglesCursorGroup(t *testing.T) {
	m := accordionModel(t)
	m.renderSessionPreviews(0)
	m.cursorSession = "$live-agent"

	handled, _ := m.handleGlobalKey(tea.KeyPressMsg{Text: "c", Code: 'c'})
	if !handled {
		t.Fatal("c should be handled in organized overview")
	}
	if !m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatal("c should collapse the cursor's group")
	}
	m.handleGlobalKey(tea.KeyPressMsg{Text: "c", Code: 'c'})
	if m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatal("c should expand the cursor's group on the second press")
	}
}

func TestGroupCollapseKeyDefersToFocusedPane(t *testing.T) {
	m := accordionModel(t)
	m.renderSessionPreviews(0)
	m.focusedSession = "$live-agent"

	// When a card is focused, c belongs to the pane (focus wins), so the global
	// handler must NOT consume it as a group toggle.
	handled, _ := m.handleGlobalKey(tea.KeyPressMsg{Text: "c", Code: 'c'})
	if handled {
		t.Fatal("c should defer to the focused pane, not toggle a group")
	}
	if m.isGroupCollapsed(groupActiveAgents.name) {
		t.Fatal("focused c must not collapse a group")
	}
}

// renderedScrollModel builds a rendered, organized model with an engaged wall
// scroll for exercising wheel/keyboard scroll routing. When tall is true the
// card viewports overflow (scrollable); otherwise they fit (unscrollable).
func renderedScrollModel(t *testing.T, tall bool) *Model {
	t.Helper()
	zone.DefaultManager = zone.New()
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 120
	m.height = 40
	m.previewOffset = 2
	m.footerHeight = 2
	m.preferredCols = 2
	m.cardCols = 2
	m.cardInnerWidth = 50
	m.cardInnerHeight = 8
	for i := 0; i < 4; i++ {
		s := agentSessionForGroup("agent-"+string(rune('a'+i)), "running")
		m.sessions = append(m.sessions, s)
		vp := viewportFor(innerDimension{width: 50, height: 8})
		if tall {
			vp.SetContent(numberedLines(60))
		} else {
			vp.SetContent("short body")
		}
		vp.GotoTop()
		m.previews[s.ID] = &sessionPreview{viewport: &vp, paneID: s.Windows[0].Panes[0].ID}
	}
	m.setAllGroupsCollapsed(orderedCockpitGroups(m, m.filteredSessions()), false)
	view := m.renderSessionPreviews(2)
	_ = zone.Scan(view)
	// Simulate an engaged wall so bubbled scroll has somewhere to go.
	m.pageScrollEngaged = true
	m.pageMaxOffset = 50
	m.pageContentHeight = 10
	return m
}

func TestWheelOverGutterScrollsWall(t *testing.T) {
	m := &Model{pageScrollEngaged: true, pageMaxOffset: 20, pageContentHeight: 10}
	// No cards: the pointer is over gutter/empty space; the wheel scrolls the wall.
	m.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.pageOffset != scrollStep {
		t.Fatalf("wheel over gutter should scroll the wall, offset=%d want %d", m.pageOffset, scrollStep)
	}
	m.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.pageOffset != 0 {
		t.Fatalf("wheel up over gutter should scroll the wall back, offset=%d", m.pageOffset)
	}
}

func TestUnfocusedPgDnScrollsWall(t *testing.T) {
	m := &Model{pageScrollEngaged: true, pageMaxOffset: 20, pageContentHeight: 10}
	handled, _ := m.handleGlobalKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !handled {
		t.Fatal("unfocused PgDn should be handled")
	}
	if m.pageOffset != m.pageStep() {
		t.Fatalf("unfocused PgDn should page the wall, offset=%d want %d", m.pageOffset, m.pageStep())
	}
}

func TestFocusedPgDnDefersWallToCard(t *testing.T) {
	m := &Model{focusedSession: "s1", pageScrollEngaged: true, pageMaxOffset: 20, pageContentHeight: 10}
	handled, _ := m.handleGlobalKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if handled {
		t.Fatal("focused PgDn should defer to the focused card (global handler returns false)")
	}
	if m.pageOffset != 0 {
		t.Fatalf("focused PgDn must not scroll the wall, offset=%d", m.pageOffset)
	}
}

func TestFocusedKeyPgDnPagesCardNotWall(t *testing.T) {
	vp := viewportFor(innerDimension{width: 20, height: 3})
	vp.SetContent(numberedLines(30))
	vp.GotoTop()
	m := &Model{
		focusedSession:    "s1",
		pageScrollEngaged: true,
		pageMaxOffset:     20,
		pageContentHeight: 10,
		previews:          map[string]*sessionPreview{"s1": {viewport: &vp, paneID: "%1"}},
		sessions: []tmux.Session{{
			ID:      "s1",
			Windows: []tmux.Window{{Active: true, Panes: []tmux.Pane{{ID: "%1", Active: true}}}},
		}},
	}
	handled, _ := m.handleFocusedKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !handled {
		t.Fatal("focused key handler should page the card")
	}
	if m.pageOffset != 0 {
		t.Fatalf("focused card PgDn must not scroll the wall, offset=%d", m.pageOffset)
	}
	if vp.AtTop() {
		t.Fatal("focused PgDn should page the card down")
	}
}

func TestWheelOverUnscrollableCardBubblesToWall(t *testing.T) {
	m := renderedScrollModel(t, false)
	if len(m.cardLayout) == 0 {
		t.Fatal("expected rendered cards")
	}
	card := m.cardLayout[0]
	info := waitForZone(t, card.zoneID)
	before := m.pageOffset

	m.handleMouse(tea.MouseWheelMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseWheelDown})
	if m.pageOffset <= before {
		t.Fatalf("wheel over an at-bottom/unscrollable card should bubble to the wall, offset %d -> %d", before, m.pageOffset)
	}
}

func TestWheelOverScrollableCardScrollsCardNotWall(t *testing.T) {
	m := renderedScrollModel(t, true)
	if len(m.cardLayout) == 0 {
		t.Fatal("expected rendered cards")
	}
	card := m.cardLayout[0]
	info := waitForZone(t, card.zoneID)
	preview := m.previews[card.sessionID]
	before := m.pageOffset

	m.handleMouse(tea.MouseWheelMsg{X: info.StartX, Y: info.StartY, Button: tea.MouseWheelDown})
	if m.pageOffset != before {
		t.Fatalf("wheel over a scrollable card should not move the wall, offset %d -> %d", before, m.pageOffset)
	}
	if preview.viewport.AtTop() {
		t.Fatal("wheel over a scrollable card should scroll the card")
	}
}

func TestGroupCollapseAllKey(t *testing.T) {
	m := accordionModel(t)
	m.renderSessionPreviews(0)
	groups := orderedCockpitGroups(m, m.filteredSessions())

	// Interactive expanded + Runtime collapsed -> some expanded -> C collapses all.
	if handled, _ := m.handleGlobalKey(tea.KeyPressMsg{Text: "C", Code: 'C'}); !handled {
		t.Fatal("C should be handled")
	}
	for _, g := range groups {
		if !m.isGroupCollapsed(g.name) {
			t.Fatalf("collapse-all should collapse %q", g.name)
		}
	}
	// All collapsed -> C expands all.
	m.handleGlobalKey(tea.KeyPressMsg{Text: "C", Code: 'C'})
	for _, g := range groups {
		if m.isGroupCollapsed(g.name) {
			t.Fatalf("expand-all should expand %q", g.name)
		}
	}
}
