// File update_test.go exercises capture scheduling and helper sizing.
package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// TestCaptureLinesFor clamps capture sizes to configured bounds.
func TestCaptureLinesFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		height int
		want   int
	}{
		{name: "zero height uses min", height: 0, want: minCaptureLines},
		{name: "small height adds slack", height: 20, want: minCaptureLines},
		{name: "large height caps", height: 1000, want: maxCaptureLines},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := captureLinesFor(tc.height); got != tc.want {
				t.Fatalf("captureLinesFor(%d) = %d, want %d", tc.height, got, tc.want)
			}
		})
	}
}

// TestEnsurePreviewsSkipsCollapsed avoids captures when cards are collapsed and unfocused.
func TestEnsurePreviewsSkipsCollapsed(t *testing.T) {
	t.Parallel()

	m := &Model{
		previews:  make(map[string]*sessionPreview),
		hidden:    make(map[string]struct{}),
		stale:     make(map[string]struct{}),
		collapsed: map[string]struct{}{"$1": {}},
		width:     120,
		height:    60,
	}
	m.sessions = []tmux.Session{{
		ID: "$1",
		Windows: []tmux.Window{{
			Active: true,
			Panes:  []tmux.Pane{{ID: "%1", Active: true, LastActivity: time.Now()}},
		}},
	}}

	if cmd := m.ensurePreviewsAndCapture(); cmd != nil {
		t.Fatalf("expected no capture command when session is collapsed and unfocused")
	}
}

// TestEnsurePreviewsCapturesDetailSessions keeps detail view content up to date even if collapsed.
func TestEnsurePreviewsCapturesDetailSessions(t *testing.T) {
	t.Parallel()

	m := &Model{
		previews:      make(map[string]*sessionPreview),
		hidden:        make(map[string]struct{}),
		stale:         make(map[string]struct{}),
		collapsed:     map[string]struct{}{"$1": {}},
		width:         120,
		height:        60,
		viewMode:      viewModeDetail,
		detailSession: "$1",
	}
	m.sessions = []tmux.Session{{
		ID: "$1",
		Windows: []tmux.Window{{
			Active: true,
			Panes:  []tmux.Pane{{ID: "%1", Active: true, LastActivity: time.Now()}},
		}},
	}}

	if cmd := m.ensurePreviewsAndCapture(); cmd == nil {
		t.Fatalf("expected capture command for detail session")
	}
}

func TestEnsurePreviewsFastCapturesActiveAgentsWithoutSpendingBackgroundBudget(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.sessions = []tmux.Session{
		captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{Kind: "agent", Agent: "fable", State: "waiting"}),
		captureTestSession("$active-2", "%active-2", tmux.CockpitMeta{Kind: "agent", Agent: "codex", State: "running"}),
		captureTestSession("$background", "%background", tmux.CockpitMeta{}),
	}

	cmd := m.ensurePreviewsAndCapture()
	if cmd == nil {
		t.Fatalf("expected capture commands")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("capture commands = %T, want tea.BatchMsg", msg)
	}
	if got, want := len(batch), 3; got != want {
		t.Fatalf("capture command count = %d, want %d", got, want)
	}
}

func TestCaptureOrderPrioritizesActiveAgentsBeforeBackgroundRotation(t *testing.T) {
	t.Parallel()

	m := &Model{
		previews:         make(map[string]*sessionPreview),
		hidden:           make(map[string]struct{}),
		stale:            make(map[string]struct{}),
		collapsed:        make(map[string]struct{}),
		classifyCache:    make(map[string]*sessionClassification),
		captureOffset:    1,
		focusedSession:   "$focused",
		cursorSession:    "$cursor",
		viewMode:         viewModeDetail,
		detailSession:    "$detail",
		artifactOutcomes: make(map[string]string),
	}
	m.sessions = []tmux.Session{
		captureTestSession("$background-0", "%background-0", tmux.CockpitMeta{}),
		captureTestSession("$active", "%active", tmux.CockpitMeta{Kind: "agent", Agent: "fable", State: "waiting"}),
		captureTestSession("$background-1", "%background-1", tmux.CockpitMeta{}),
		captureTestSession("$focused", "%focused", tmux.CockpitMeta{}),
		captureTestSession("$detail", "%detail", tmux.CockpitMeta{}),
		captureTestSession("$cursor", "%cursor", tmux.CockpitMeta{}),
	}

	order := m.captureOrder()
	var got []string
	for _, session := range order {
		got = append(got, session.ID)
	}
	wantPrefix := []string{"$focused", "$detail", "$cursor", "$active"}
	if len(got) < len(wantPrefix) {
		t.Fatalf("capture order too short: %#v", got)
	}
	for i, want := range wantPrefix {
		if got[i] != want {
			t.Fatalf("capture order prefix = %#v, want prefix %#v", got[:len(wantPrefix)], wantPrefix)
		}
	}
}

func captureTestSession(sessionID, paneID string, meta tmux.CockpitMeta) tmux.Session {
	pane := tmux.Pane{ID: paneID, Active: true, LastActivity: time.Now()}
	if meta.HasData() {
		pane.Cockpit = &meta
	}
	return tmux.Session{
		ID:   sessionID,
		Name: strings.TrimPrefix(sessionID, "$"),
		Windows: []tmux.Window{{
			ID:     sessionID + ":w",
			Active: true,
			Panes:  []tmux.Pane{pane},
		}},
	}
}

func TestEnsurePreviewsUsesSyntheticPreviewText(t *testing.T) {
	t.Parallel()

	lines := make([]string, 16)
	for i := range lines {
		lines[i] = fmt.Sprintf("runtime line %02d", i)
	}
	content := strings.Join(lines, "\n")
	m := &Model{
		previews:  make(map[string]*sessionPreview),
		hidden:    make(map[string]struct{}),
		stale:     make(map[string]struct{}),
		collapsed: make(map[string]struct{}),
		width:     120,
		height:    60,
	}
	m.sessions = []tmux.Session{{
		ID: "$synthetic",
		Windows: []tmux.Window{{
			Active: true,
			Panes: []tmux.Pane{{
				ID:           "%synthetic",
				Active:       true,
				LastActivity: time.Now(),
				PreviewText:  content,
			}},
		}},
	}}

	if cmd := m.ensurePreviewsAndCapture(); cmd != nil {
		t.Fatalf("expected no tmux capture command for synthetic preview text")
	}
	preview := m.previews["$synthetic"]
	if preview == nil {
		t.Fatalf("expected preview to be created")
	}
	if got := preview.lastContent; got != content {
		t.Fatalf("lastContent = %q", got)
	}
	if !preview.viewport.AtBottom() {
		t.Fatalf("synthetic preview text should anchor to bottom on initial load")
	}
}

func TestEnsurePreviewsPrunesDisappearedRuntimeCard(t *testing.T) {
	t.Parallel()

	vp := viewportFor(innerDimension{width: 80, height: 5})
	m := &Model{
		previews: map[string]*sessionPreview{
			"openclaw-runtime:old-card": {
				viewport:    &vp,
				paneID:      "%openclaw-runtime:old-card",
				lastContent: "old runtime card",
			},
		},
		hidden:    make(map[string]struct{}),
		stale:     make(map[string]struct{}),
		collapsed: make(map[string]struct{}),
		width:     120,
		height:    60,
	}

	if cmd := m.ensurePreviewsAndCapture(); cmd != nil {
		t.Fatalf("expected no capture command with no sessions")
	}
	if _, ok := m.previews["openclaw-runtime:old-card"]; ok {
		t.Fatalf("disappeared runtime card preview should be pruned")
	}
}

func TestSnapshotPrunesHiddenDisappearedRuntimeCard(t *testing.T) {
	t.Parallel()

	m := &Model{
		previews:  make(map[string]*sessionPreview),
		hidden:    map[string]struct{}{"openclaw-runtime:old-card": {}, "$normal": {}},
		stale:     make(map[string]struct{}),
		collapsed: make(map[string]struct{}),
		width:     120,
		height:    60,
	}
	m.Update(snapshotMsg{snapshot: tmux.Snapshot{
		Timestamp: time.Now(),
		Sessions: []tmux.Session{{
			ID: "$normal",
			Windows: []tmux.Window{{
				Active: true,
				Panes:  []tmux.Pane{{ID: "%normal", Active: true, LastActivity: time.Now()}},
			}},
		}},
	}})

	if _, ok := m.hidden["openclaw-runtime:old-card"]; ok {
		t.Fatalf("disappeared runtime card hidden state should be pruned")
	}
	if _, ok := m.hidden["$normal"]; !ok {
		t.Fatalf("non-runtime hidden state should remain")
	}
}

// TestPaneContentRespectsManualScroll keeps manual offsets when the user scrolls away from the bottom.
func TestPaneContentRespectsManualScroll(t *testing.T) {
	t.Parallel()

	lines := make([]string, 16)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%02d", i)
	}
	content := strings.Join(lines, "\n")
	vp := viewportFor(innerDimension{width: 80, height: 6})
	vp.SetContent(content)
	vp.GotoBottom()
	vp.ScrollUp(3)
	if vp.AtBottom() {
		t.Fatalf("expected viewport not at bottom after scrolling up")
	}
	preview := &sessionPreview{
		viewport:    &vp,
		paneID:      "%1",
		lastContent: content,
		lastChanged: time.Now(),
		autoFollow:  false,
	}
	m := &Model{
		previews: map[string]*sessionPreview{
			"$1": preview,
		},
		stale: make(map[string]struct{}),
	}
	originalOffset := preview.viewport.YOffset()

	msg := paneContentMsg{
		sessionID: "$1",
		paneID:    "%1",
		text:      content + "\nextra-line",
	}
	m.Update(msg)

	if got := preview.viewport.YOffset(); got != originalOffset {
		t.Fatalf("unexpected YOffset, got %d want %d", got, originalOffset)
	}
	if preview.viewport.AtBottom() {
		t.Fatalf("viewport jumped to bottom after manual scroll")
	}
	wantContent := strings.TrimRight(msg.text, "\n")
	if got := preview.lastContent; got != wantContent {
		t.Fatalf("lastContent = %q, want %q", got, wantContent)
	}
}

// TestPaneContentInitialCaptureFollowsBottom keeps newly discovered panes anchored to latest output.
func TestPaneContentInitialCaptureFollowsBottom(t *testing.T) {
	t.Parallel()

	lines := make([]string, 16)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%02d", i)
	}
	content := strings.Join(lines, "\n")
	vp := viewportFor(innerDimension{width: 80, height: 6})
	preview := &sessionPreview{
		viewport:    &vp,
		paneID:      "%1",
		lastChanged: time.Now(),
		autoFollow:  true,
	}
	m := &Model{
		previews: map[string]*sessionPreview{
			"$1": preview,
		},
		stale: make(map[string]struct{}),
	}

	msg := paneContentMsg{
		sessionID: "$1",
		paneID:    "%1",
		text:      content,
	}
	m.Update(msg)

	if !preview.viewport.AtBottom() {
		t.Fatalf("expected initial content capture to jump to bottom")
	}
	if !preview.autoFollow {
		t.Fatalf("expected autoFollow to remain enabled")
	}
}

// TestPaneContentFollowsBottom continues auto-follow when the viewport was already at the bottom.
func TestPaneContentFollowsBottom(t *testing.T) {
	t.Parallel()

	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%02d", i)
	}
	content := strings.Join(lines, "\n")
	vp := viewportFor(innerDimension{width: 80, height: 6})
	vp.SetContent(content)
	vp.GotoBottom()
	if !vp.AtBottom() {
		t.Fatalf("expected viewport to be at bottom initially")
	}

	preview := &sessionPreview{
		viewport:    &vp,
		paneID:      "%1",
		lastContent: content,
		lastChanged: time.Now(),
		autoFollow:  true,
	}
	m := &Model{
		previews: map[string]*sessionPreview{
			"$1": preview,
		},
		stale: make(map[string]struct{}),
	}

	msg := paneContentMsg{
		sessionID: "$1",
		paneID:    "%1",
		text:      content + "\nextra-line",
	}
	m.Update(msg)

	if !preview.viewport.AtBottom() {
		t.Fatalf("expected viewport to remain at bottom when already there")
	}
	wantContent := strings.TrimRight(msg.text, "\n")
	if got := preview.lastContent; got != wantContent {
		t.Fatalf("lastContent = %q, want %q", got, wantContent)
	}
}
