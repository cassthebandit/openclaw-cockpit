package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	"github.com/mattn/go-runewidth"
)

func TestCaptureOwnershipAcrossPathsAndReplacement(t *testing.T) {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.sessions = []tmux.Session{captureTestSession("$worker", "%old", tmux.CockpitMeta{Kind: "agent", State: "running"})}
	m.ensurePreviewsAndCapture()
	old := m.previews["$worker"].captureGeneration
	spent := m.captureTokensSpent
	if requests := m.planFastCaptures(); len(requests) != 0 || m.captureTokensSpent != spent {
		t.Fatal("fast path overlapped snapshot capture")
	}
	m.sessions[0].Windows[0].Panes[0].ID = "%new"
	m.ensurePreviewsAndCapture()
	newer := m.previews["$worker"].captureGeneration
	m.Update(paneContentMsg{sessionID: "$worker", paneID: "%old", generation: old, text: "obsolete"})
	if _, ok := m.fastCaptureActive["$worker"]; !ok {
		t.Fatal("old pane released new request")
	}
	m.Update(paneContentMsg{sessionID: "$worker", paneID: "%new", generation: newer, text: "newest"})
	m.Update(paneContentMsg{sessionID: "$worker", paneID: "%new", generation: old, text: "obsolete"})
	if got := m.previews["$worker"].lastContent; got != "newest" {
		t.Fatalf("late response won: %q", got)
	}
	// Fast admission also prevents snapshot admission. A duplicate completion
	// cannot release a subsequent request on the same pane.
	requests := m.planFastCaptures()
	if len(requests) != 1 {
		t.Fatalf("fast requests=%d", len(requests))
	}
	spent = m.captureTokensSpent
	m.ensurePreviewsAndCapture()
	if m.captureTokensSpent != spent {
		t.Fatal("snapshot overlapped fast capture")
	}
	m.Update(paneContentMsg{sessionID: "$worker", paneID: "%new", generation: newer, text: "duplicate"})
	if _, ok := m.fastCaptureActive["$worker"]; !ok {
		t.Fatal("duplicate released current request")
	}
	m.Update(paneContentMsg{sessionID: "$worker", paneID: "%new", generation: requests[0].generation, text: "last"})
	if m.previews["$worker"].lastContent != "last" {
		t.Fatal("current response rejected")
	}
}

func TestRuntimeStructuredFailureAndSourceGroup(t *testing.T) {
	script := writeRuntimeScript(t, `import json,sys
print(json.dumps({"ok":False,"error":"missing table tasks\u001b[2J"}))
sys.exit(1)
`)
	sessions := openClawRuntimeSessions(RuntimeSource{Enabled: true, Script: script}, time.Now())
	if len(sessions) != 1 {
		t.Fatal(sessions)
	}
	if group := cockpitGroupFor(nil, sessions[0]); group != groupSubsystemFailures {
		t.Fatalf("wrong failure group: %+v", group)
	}
	text := sessions[0].Windows[0].Panes[0].PreviewText
	if !strings.Contains(text, "missing table tasks") || strings.Contains(text, "\x1b") {
		t.Fatalf("lost/unsafe error: %q", text)
	}
}

func TestRuntimeDeliveredCountsAtBothCaps(t *testing.T) {
	for _, producerCount := range []int{3, 9} {
		script := writeRuntimeScript(t, `import json
cards=[{"id":str(i),"cardContract":"runtime-card.v1"} for i in range(`+intString(producerCount)+`)]
print(json.dumps({"cardContract":"runtime-card.v1","summary":{"visibleRuntimeCardCount":9},"cards":cards}))
`)
		cards, err := loadOpenClawRuntimeCards(RuntimeSource{Script: script, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		summary := cards[0].summary
		if summary.VisibleRuntimeCardCount != len(cards) || summary.TotalVisibleRuntimeCardCount != 9 {
			t.Fatalf("count mismatch: %+v cards=%d", summary, len(cards))
		}
	}
}

func TestCompletedAssignmentOutranksOldScreenButNotProcessFailure(t *testing.T) {
	now := time.Now()
	pane := tmux.Pane{PreviewText: "Working… press escape to interrupt", Cockpit: &tmux.CockpitMeta{Kind: "agent", ManagedBy: "agent_wall", State: "done", StartedAt: now.Add(-time.Hour).Format(time.RFC3339), CompletedAt: now.Format(time.RFC3339)}}
	session := tmux.Session{Windows: []tmux.Window{{Panes: []tmux.Pane{pane}}}}
	if state := paneAttentionState(nil, session, pane); state != "done" {
		t.Fatal(state)
	}
	pane.Dead = true
	pane.DeadStatus = 1
	if state := paneAttentionState(nil, session, pane); state != "failed" {
		t.Fatal(state)
	}
	pane.Dead = false
	pane.Cockpit.State = "running"
	pane.Cockpit.CompletedAt = ""
	if state := paneAttentionState(nil, session, pane); state == "done" {
		t.Fatal("invented completion")
	}
}

func TestUnicodeCenteredByCells(t *testing.T) {
	got := centerText("界面", 10)
	if got != "   界面" || runewidth.StringWidth(got) != 7 {
		t.Fatalf("centered %q", got)
	}
}

func TestGroupedNavigationMatchesRenderedRows(t *testing.T) {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 220
	m.height = 80
	m.preferredCols = 4
	for i := 0; i < 5; i++ {
		m.sessions = append(m.sessions, captureTestSession("$agent-"+intString(i+1), "%agent-"+intString(i+1), tmux.CockpitMeta{Kind: "agent", State: "running"}))
	}
	for i := 0; i < 7; i++ {
		session := sessionForGroup("route-"+intString(i+1), "openclaw-runtime", "", "route")
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
		m.sessions = append(m.sessions, session)
	}
	for i := range m.sessions {
		for j := range m.sessions[i].Windows {
			for k := range m.sessions[i].Windows[j].Panes {
				m.sessions[i].Windows[j].Panes[k].LastActivity = time.Time{}
			}
		}
	}
	for _, session := range m.sessions {
		seedPreviewForSizingTest(m, session, 80, 20)
	}
	m.updatePreviewDimensions(len(m.sessions))
	m.renderSessionCards(m.filteredSessions())
	if len(m.cardLayout) != len(m.sessions) {
		t.Fatalf("rendered %d/%d cards", len(m.cardLayout), len(m.sessions))
	}
	m.cursorSession = "$agent-5"
	if !m.moveCursorDown() || m.cursorSession != "$agent-2" {
		t.Fatalf("down used global cols: %s", m.cursorSession)
	}
	m.cursorSession = "$agent-3"
	if !m.moveCursorUp() || m.cursorSession != "$agent-5" {
		t.Fatalf("partial row did not pick nearest card: %s", m.cursorSession)
	}
	cells := m.cursorCells()
	for _, cell := range cells {
		for _, bounds := range m.cardLayout {
			if bounds.sessionID != cell.id {
				continue
			}
			if center := (bounds.screenX0 + bounds.screenX1 + 1) / 2; center != cell.center {
				t.Fatalf("draw/navigation centers disagree %s: %d/%d", cell.id, center, cell.center)
			}
		}
	}
	m.toggleGroupCollapsed(groupActiveAgents.name)
	if cells := m.cursorCells(); len(cells) != 7 {
		t.Fatalf("collapsed group navigation has %d cards", len(cells))
	}
	m.width = 90
	cells = m.cursorCells()
	if cells[0].row == cells[2].row {
		t.Fatal("resize did not recompute columns")
	}
}
