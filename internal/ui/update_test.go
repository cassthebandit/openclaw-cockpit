// File update_test.go exercises capture scheduling and helper sizing.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestFastCaptureTickDoesNotFetchFullRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.SetOpenClawRuntimeSource("/tmp/should-not-run-at-fast-rate.py", 10, 5*time.Second)
	m.sessions = []tmux.Session{
		captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{ManagedBy: "agent_wall", Kind: "agent", Agent: "fable", State: "waiting"}),
	}
	vp := viewportFor(innerDimension{width: 80, height: 8})
	m.previews["$active-1"] = &sessionPreview{viewport: &vp, paneID: "%active-1", lastContent: "ready"}

	_, cmd := m.Update(fastTickMsg{})
	if cmd == nil {
		t.Fatalf("expected fast tick command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("fast tick command = %T, want tea.BatchMsg", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("fast tick batch len = %d, want schedule + pane capture only", len(batch))
	}
}

func TestOpenClawRuntimeSourceUsesFiveSecondFallback(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.SetOpenClawRuntimeSource("/tmp/runtime-snapshot.py", 10, time.Second)
	if got := m.runtime.Interval; got != 5*time.Second {
		t.Fatalf("runtime fallback interval = %s, want 5s", got)
	}
}

func TestFastCaptureSkipsPaneAlreadyInFlight(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.sessions = []tmux.Session{
		captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{ManagedBy: "agent_wall", Kind: "agent", Agent: "fable", State: "waiting"}),
	}
	vp := viewportFor(innerDimension{width: 80, height: 8})
	m.previews["$active-1"] = &sessionPreview{viewport: &vp, paneID: "%active-1", lastContent: "ready"}

	first := m.ensureFastCaptures()
	if first == nil {
		t.Fatalf("expected first fast capture command")
	}
	if _, ok := m.fastCaptureActive["$active-1"]; !ok {
		t.Fatalf("expected fast capture in-flight marker")
	}
	if second := m.ensureFastCaptures(); second != nil {
		t.Fatalf("expected no overlapping fast capture command")
	}
	m.Update(paneContentMsg{sessionID: "$active-1", paneID: "%active-1", text: "done"})
	if _, ok := m.fastCaptureActive["$active-1"]; ok {
		t.Fatalf("expected fast capture in-flight marker to clear")
	}
	if third := m.ensureFastCaptures(); third != nil {
		t.Fatalf("expected no immediate fallback fast capture without a pane log signal")
	}
	time.Sleep(260 * time.Millisecond)
	if fourth := m.ensureFastCaptures(); fourth == nil {
		t.Fatalf("expected fallback fast capture after throttle window")
	}
}

func TestFastCaptureWaitsForPaneLogSignal(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "active-pane.log")
	if err := os.WriteFile(logPath, []byte("ready\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.sessions = []tmux.Session{
		captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{
			ManagedBy: "agent_wall",
			Kind:      "agent",
			Agent:     "fable",
			State:     "waiting",
			PaneLog:   logPath,
		}),
	}
	vp := viewportFor(innerDimension{width: 80, height: 8})
	m.previews["$active-1"] = &sessionPreview{viewport: &vp, paneID: "%active-1", lastContent: "ready"}

	first := m.ensureFastCaptures()
	if first == nil {
		t.Fatalf("expected initial capture to seed signal state")
	}
	m.Update(paneContentMsg{sessionID: "$active-1", paneID: "%active-1", text: "ready"})
	if second := m.ensureFastCaptures(); second != nil {
		t.Fatalf("expected unchanged pane log to suppress fast capture")
	}
	if err := os.WriteFile(logPath, []byte("ready\nchanged\n"), 0o644); err != nil {
		t.Fatalf("update log: %v", err)
	}
	if third := m.ensureFastCaptures(); third == nil {
		t.Fatalf("expected changed pane log to trigger fast capture")
	}
}

// TestSnapshotDoesNotSpawnSecondWatcherLineage is the P0-1 regression: while a
// fast watcher command is outstanding, snapshot ticks must not arm another
// lineage, and each fastTickMsg must arm exactly one successor.
func TestSnapshotDoesNotSpawnSecondWatcherLineage(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.sessions = []tmux.Session{
		captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{ManagedBy: "agent_wall", Kind: "agent", Agent: "fable", State: "waiting"}),
	}

	first := m.scheduleFastCaptureWatch()
	if first == nil {
		t.Fatalf("expected initial watcher to be armed")
	}
	if !m.fastWatchActive || m.fastWatchGen != 1 {
		t.Fatalf("watcher state = active %v gen %d, want active true gen 1", m.fastWatchActive, m.fastWatchGen)
	}
	if second := m.scheduleFastCaptureWatch(); second != nil {
		t.Fatalf("expected no second watcher while one is outstanding")
	}

	m.Update(snapshotMsg{snapshot: tmux.Snapshot{Timestamp: time.Now(), Sessions: m.sessions}})
	if m.fastWatchGen != 1 {
		t.Fatalf("snapshot spawned a watcher lineage: gen = %d, want 1", m.fastWatchGen)
	}
	if !m.fastWatchActive {
		t.Fatalf("outstanding watcher lost its single-flight marker")
	}

	m.Update(fastTickMsg{})
	if m.fastWatchGen != 2 {
		t.Fatalf("fast tick armed %d watchers total, want exactly one successor (gen 2)", m.fastWatchGen)
	}
	if !m.fastWatchActive {
		t.Fatalf("expected successor watcher to be outstanding after fast tick")
	}
}

// TestFastCaptureSignalsExcludeInFlightAndCollapsed is the P0-2 regression:
// the watcher's signal set must exclude sessions the dispatch path would skip,
// so a non-dispatchable signal change can never re-fire the watcher.
func TestFastCaptureSignalsExcludeInFlightAndCollapsed(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	dir := t.TempDir()
	ids := []string{"$inflight", "$collapsed", "$eligible"}
	for _, id := range ids {
		name := strings.TrimPrefix(id, "$")
		logPath := filepath.Join(dir, name+"-pane.log")
		if err := os.WriteFile(logPath, []byte("ready\n"), 0o644); err != nil {
			t.Fatalf("write log: %v", err)
		}
		paneID := "%" + name
		m.sessions = append(m.sessions, captureTestSession(id, paneID, tmux.CockpitMeta{
			ManagedBy: "agent_wall",
			Kind:      "agent",
			Agent:     "fable",
			State:     "running",
			PaneLog:   logPath,
		}))
		vp := viewportFor(innerDimension{width: 80, height: 8})
		m.previews[id] = &sessionPreview{viewport: &vp, paneID: paneID, lastContent: "ready"}
	}
	m.fastCaptureActive["$inflight"] = struct{}{}
	m.collapsed["$collapsed"] = struct{}{}

	signals, missingSignal, inflightSkipped := m.fastCaptureSignals()
	if len(signals) != 1 || signals[0].sessionID != "$eligible" {
		t.Fatalf("signal set = %#v, want only $eligible", signals)
	}
	if !inflightSkipped {
		t.Fatalf("expected in-flight session to report inflightSkipped")
	}
	if missingSignal {
		t.Fatalf("unexpected missing-signal flag with pane logs present")
	}
}

// TestFastCaptureStatErrorRecordsSignalState is the P0-2 regression for the
// error path: a failing pane-log stat must be recorded as observed state so
// the watcher treats it as known-bad instead of firing on every sweep, and a
// reappearing log must read as a change.
func TestFastCaptureStatErrorRecordsSignalState(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	logPath := filepath.Join(t.TempDir(), "gone", "pane.log")
	m.sessions = []tmux.Session{
		captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{
			ManagedBy: "agent_wall",
			Kind:      "agent",
			Agent:     "fable",
			State:     "running",
			PaneLog:   logPath,
		}),
	}
	vp := viewportFor(innerDimension{width: 80, height: 8})
	m.previews["$active-1"] = &sessionPreview{viewport: &vp, paneID: "%active-1", lastContent: "ready"}

	if cmd := m.ensureFastCaptures(); cmd == nil {
		t.Fatalf("expected fallback capture dispatch on first stat error")
	}
	preview := m.previews["$active-1"]
	if !preview.signal.statErr || !preview.signal.seen || preview.signal.path != logPath {
		t.Fatalf("stat error not recorded as signal state: %+v", preview.signal)
	}
	m.Update(paneContentMsg{sessionID: "$active-1", paneID: "%active-1", text: "ready"})

	signals, _, inflightSkipped := m.fastCaptureSignals()
	if inflightSkipped {
		t.Fatalf("capture completion should clear the in-flight marker")
	}
	if len(signals) != 1 || !signals[0].statErr || !signals[0].seen {
		t.Fatalf("signal entry = %#v, want recorded stat-error state", signals)
	}
	if fastCaptureSignalsDirty(signals) {
		t.Fatalf("known-bad pane log must not read as a signal change")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("back\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if !fastCaptureSignalsDirty(signals) {
		t.Fatalf("reappearing pane log must read as a signal change")
	}
}

// TestFastCaptureWatchSleepsBeforeReturning is the P0-2 no-spin invariant: the
// watcher command must never return faster than one sweep interval, even when
// its signal set is dirty from the start.
func TestFastCaptureWatchSleepsBeforeReturning(t *testing.T) {
	t.Parallel()

	signals := []fastCaptureSignal{{
		sessionID: "$a",
		path:      filepath.Join(t.TempDir(), "missing-pane.log"),
	}}
	cmd := scheduleFastCaptureWatch(signals, fastCaptureIdleTick, fastCaptureIdleTick)
	start := time.Now()
	msg := cmd()
	elapsed := time.Since(start)
	if _, ok := msg.(fastTickMsg); !ok {
		t.Fatalf("watch returned %T, want fastTickMsg", msg)
	}
	if elapsed < fastCaptureInterval {
		t.Fatalf("watcher returned after %s without sleeping a sweep interval (%s)", elapsed, fastCaptureInterval)
	}
}

// TestFastCaptureWatchKnownStatErrorHoldsUntilDeadline verifies a persistently
// missing pane log with recorded stat-error state does not fire the watcher
// early; the watch runs to its idle deadline.
func TestFastCaptureWatchKnownStatErrorHoldsUntilDeadline(t *testing.T) {
	t.Parallel()

	signals := []fastCaptureSignal{{
		sessionID: "$a",
		path:      filepath.Join(t.TempDir(), "missing-pane.log"),
		seen:      true,
		statErr:   true,
	}}
	deadline := 60 * time.Millisecond
	cmd := scheduleFastCaptureWatch(signals, fastCaptureIdleTick, deadline)
	start := time.Now()
	msg := cmd()
	elapsed := time.Since(start)
	if _, ok := msg.(fastTickMsg); !ok {
		t.Fatalf("watch returned %T, want fastTickMsg", msg)
	}
	if elapsed < deadline {
		t.Fatalf("watcher fired after %s on a known-bad signal, want to hold for %s", elapsed, deadline)
	}
}

// TestSnapshotMergesCachedRuntimeCards is the P1-1 regression: snapshots carry
// tmux sessions only and runtime cards merge from the async loader's cache, so
// the runtime script is never on the snapshot path.
func TestSnapshotMergesCachedRuntimeCards(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.SetOpenClawRuntimeSource("/nonexistent/should-not-run.py", 10, time.Second)
	cached := tmux.Session{
		ID:   "openclaw-runtime:cached-card",
		Name: "cached card",
		Windows: []tmux.Window{{
			ID:     "@openclaw-runtime:cached-card",
			Active: true,
			Panes:  []tmux.Pane{{ID: "%openclaw-runtime:cached-card", Active: true, LastActivity: time.Now()}},
		}},
	}
	m.runtimeSessions = []tmux.Session{cached}

	m.Update(snapshotMsg{snapshot: tmux.Snapshot{
		Timestamp: time.Now(),
		Sessions: []tmux.Session{
			captureTestSession("$normal", "%normal", tmux.CockpitMeta{}),
		},
	}})

	if len(m.sessions) != 2 {
		t.Fatalf("merged session count = %d, want tmux session + cached runtime card", len(m.sessions))
	}
	if !m.sessionExists("$normal") || !m.sessionExists("openclaw-runtime:cached-card") {
		t.Fatalf("merged sessions missing tmux or cached runtime entry: %#v", m.sessions)
	}
}

// TestRuntimeCardRefreshIsSingleFlightAndReschedules covers the P1-1 refresh
// loop: ticks are ignored while a load is in flight or the source is disabled,
// and a completed load replaces the cache and re-arms the next tick.
func TestRuntimeCardRefreshIsSingleFlightAndReschedules(t *testing.T) {
	t.Parallel()

	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)

	if _, cmd := m.Update(runtimeTickMsg{}); cmd != nil {
		t.Fatalf("runtime tick must be a no-op while the source is disabled")
	}

	m.SetOpenClawRuntimeSource("/nonexistent/should-not-run.py", 10, time.Second)
	_, cmd := m.Update(runtimeTickMsg{})
	if cmd == nil || !m.runtimeInflight {
		t.Fatalf("expected runtime tick to arm a single load")
	}
	if _, second := m.Update(runtimeTickMsg{}); second != nil {
		t.Fatalf("expected no overlapping runtime load")
	}

	cards := []tmux.Session{{ID: "openclaw-runtime:new-card", Name: "new card"}}
	_, rearm := m.Update(runtimeCardsMsg{sessions: cards, loadedAt: time.Now()})
	if rearm == nil {
		t.Fatalf("expected completed runtime load to schedule the next tick")
	}
	if m.runtimeInflight {
		t.Fatalf("expected completed runtime load to clear the in-flight marker")
	}
	if len(m.runtimeSessions) != 1 || m.runtimeSessions[0].ID != "openclaw-runtime:new-card" {
		t.Fatalf("runtime card cache = %#v, want replaced by the new load", m.runtimeSessions)
	}
}

func TestPaneOutputSignalPathInfersAgentWallLog(t *testing.T) {
	t.Parallel()

	session := captureTestSession("$active-1", "%active-1", tmux.CockpitMeta{
		ManagedBy: "agent_wall",
		Kind:      "agent",
		Agent:     "fable",
		State:     "waiting",
		RunRoot:   "/tmp/run-root",
	})
	window, _ := activeWindow(session)
	pane, _ := activePane(window)

	got := paneOutputSignalPath(session, pane)
	want := "/tmp/run-root/logs/active-1-pane.log"
	if got != want {
		t.Fatalf("paneOutputSignalPath() = %q, want %q", got, want)
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
