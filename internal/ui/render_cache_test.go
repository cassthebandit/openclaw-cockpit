package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// frozenWallModel builds the bench wall with a deterministic, movable clock.
// All time anchors are pinned relative to the frozen instant so no scheduled
// transition can fire unless a test advances the clock itself.
func frozenWallModel(tb testing.TB, agents, runtimes int) (*Model, *time.Time) {
	tb.Helper()
	m := benchWallModel(tb, agents, runtimes)
	now := time.Now()
	clock := &now
	m.clock = func() time.Time { return *clock }
	m.lastUpdated = now.Add(-2 * time.Second) // "refreshed just now"; next bin at +3s
	for _, preview := range m.previews {
		preview.lastChanged = now.Add(-10 * time.Minute) // pulse long expired
	}
	return m, clock
}

// TestNoopHeartbeatMessagesDoNotRebuild is the F1 no-op regression test:
// render-neutral heartbeats (fastTickMsg, tickMsg, runtimeTickMsg) must not
// increment the render build counter while the clock is frozen.
func TestNoopHeartbeatMessagesDoNotRebuild(t *testing.T) {
	m, _ := frozenWallModel(t, 12, 4)
	_ = m.View()
	builds := m.renderBuilds

	for i := 0; i < 120; i++ {
		m.Update(fastTickMsg{})
		_ = m.View()
	}
	m.Update(tickMsg{})
	_ = m.View()
	m.Update(runtimeTickMsg{})
	_ = m.View()

	if m.renderBuilds != builds {
		t.Fatalf("render-neutral heartbeats rebuilt the frame: builds %d -> %d", builds, m.renderBuilds)
	}
}

// TestFastTickMessageMixDoesNotRebuild replays the streaming message mix:
// heartbeat ticks with render-neutral capture bookkeeping stay cached until a
// dirty capture message with actually changed content arrives; a same-content
// capture delivery stays clean.
func TestFastTickMessageMixDoesNotRebuild(t *testing.T) {
	m, _ := frozenWallModel(t, 12, 4)
	_ = m.View()
	target := m.sessions[0]
	paneID := target.Windows[0].Panes[0].ID

	builds := m.renderBuilds
	for i := 0; i < 60; i++ {
		m.Update(fastTickMsg{})
		_ = m.View()
	}
	if m.renderBuilds != builds {
		t.Fatalf("heartbeat ticks rebuilt the frame: builds %d -> %d", builds, m.renderBuilds)
	}

	m.Update(paneContentMsg{generation: m.previews[target.ID].captureGeneration, sessionID: target.ID, paneID: paneID, text: "fresh streaming output\nline 2"})
	_ = m.View()
	if m.renderBuilds != builds+1 {
		t.Fatalf("changed capture content should rebuild exactly once: builds %d -> %d", builds, m.renderBuilds)
	}

	builds = m.renderBuilds
	m.Update(paneContentMsg{generation: m.previews[target.ID].captureGeneration, sessionID: target.ID, paneID: paneID, text: "fresh streaming output\nline 2"})
	_ = m.View()
	if m.renderBuilds != builds {
		t.Fatalf("same-content capture should stay render-neutral: builds %d -> %d", builds, m.renderBuilds)
	}
}

// TestDirtyMessagesRebuild verifies deny-by-default: every message type
// outside the proven render-neutral set increments the build counter, and an
// unknown message type is dirty.
func TestDirtyMessagesRebuild(t *testing.T) {
	type unknownMsg struct{}
	messages := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "window-size", msg: tea.WindowSizeMsg{Width: 220, Height: 60}},
		{name: "key-press", msg: tea.KeyPressMsg{Text: "j", Code: 'j'}},
		{name: "mouse-motion", msg: tea.MouseMotionMsg{X: 1, Y: 1}},
		{name: "status", msg: statusMsg("toast text")},
		{name: "err", msg: errMsg{err: fmt.Errorf("boom")}},
		{name: "search-blur", msg: searchBlurMsg{}},
		{name: "snapshot", msg: snapshotMsg{snapshot: tmux.Snapshot{Timestamp: time.Now()}}},
		{name: "runtime-cards", msg: runtimeCardsMsg{loadedAt: time.Now()}},
		{name: "pane-vars", msg: paneVarsMsg{sessionID: "$x", paneID: "%x"}},
		{name: "unknown", msg: unknownMsg{}},
	}
	for _, tc := range messages {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m, _ := frozenWallModel(t, 3, 0)
			_ = m.View()
			builds := m.renderBuilds
			m.Update(tc.msg)
			_ = m.View()
			if m.renderBuilds != builds+1 {
				t.Fatalf("%s should dirty the frame: builds %d -> %d", tc.name, builds, m.renderBuilds)
			}
		})
	}
}

// TestCachedViewMatchesFreshRebuild is the byte-equality proof across the
// state matrix: for each state, a cached View() returns exactly the bytes a
// forced fresh rebuild produces.
func TestCachedViewMatchesFreshRebuild(t *testing.T) {
	now := time.Now()
	scenarios := []struct {
		name   string
		mutate func(m *Model, clock *time.Time)
	}{
		{name: "base", mutate: func(m *Model, clock *time.Time) {}},
		{name: "hover", mutate: func(m *Model, clock *time.Time) {
			m.hoveredSession = "$agent-live-00"
			m.hoveredControl = fmt.Sprintf("%smax:%s", m.zonePrefix, "$agent-live-00")
		}},
		{name: "cursor", mutate: func(m *Model, clock *time.Time) {
			m.cursorSession = "$agent-idle-01"
		}},
		{name: "collapse-card", mutate: func(m *Model, clock *time.Time) {
			m.collapsed["$agent-live-00"] = struct{}{}
		}},
		{name: "detail", mutate: func(m *Model, clock *time.Time) {
			m.enterDetail("$agent-live-00")
		}},
		{name: "filter", mutate: func(m *Model, clock *time.Time) {
			m.searchQuery = "agent-live"
		}},
		{name: "toast", mutate: func(m *Model, clock *time.Time) {
			m.showToast("cache proof toast")
		}},
		{name: "pulse", mutate: func(m *Model, clock *time.Time) {
			m.previews["$agent-live-00"].lastChanged = *clock
		}},
		{name: "stale", mutate: func(m *Model, clock *time.Time) {
			m.sessions[0] = withActivity(m.sessions[0], clock.Add(-2*time.Hour))
			m.updateStaleSessions()
			m.invalidateClassifications()
		}},
		{name: "time-bins", mutate: func(m *Model, clock *time.Time) {
			m.sessions[0] = withActivity(m.sessions[0], clock.Add(-7*time.Minute))
			m.sessions[1].Windows[0].Panes[0].Cockpit.CompletedAt = clock.Add(-11 * time.Minute).UTC().Format(time.RFC3339)
		}},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			m, clock := frozenWallModel(t, 12, 4)
			*clock = now
			sc.mutate(m, clock)
			m.markRenderDirty()

			first := m.View()
			builds := m.renderBuilds
			cached := m.View()
			if m.renderBuilds != builds {
				t.Fatalf("second View rebuilt despite clean state")
			}
			if cached.Content != first.Content {
				t.Fatalf("cached view bytes differ from built view bytes")
			}
			m.markRenderDirty()
			fresh := m.View()
			if m.renderBuilds != builds+1 {
				t.Fatalf("forced dirty View did not rebuild")
			}
			if fresh.Content != first.Content {
				t.Fatalf("fresh rebuild bytes differ from cached bytes for identical state")
			}
		})
	}
}

// TestTimeBinTransitionsRebuild proves scheduled invalidation: the cached
// frame is served up to the computed transition instant and rebuilt with
// changed label text once the clock crosses it (refreshed / last / done bins).
func TestTimeBinTransitionsRebuild(t *testing.T) {
	m, clock := frozenWallModel(t, 3, 0)
	start := *clock
	// Anchor a "done Xm" label mid-bin and drop the other card anchors.
	m.sessions[0].Windows[0].Panes[0].Cockpit.CompletedAt = start.Add(-11*time.Minute - 30*time.Second).UTC().Format(time.RFC3339)

	m.markRenderDirty()
	first := m.View()
	if m.nextRenderAt.IsZero() {
		t.Fatal("expected a scheduled render transition for time-bin labels")
	}
	if !m.nextRenderAt.After(start) {
		t.Fatalf("transition %v not in the future of %v", m.nextRenderAt, start)
	}

	// Just before the transition: still cached, byte-identical.
	*clock = m.nextRenderAt.Add(-time.Millisecond)
	builds := m.renderBuilds
	if got := m.View(); got.Content != first.Content || m.renderBuilds != builds {
		t.Fatal("frame changed before the scheduled bin edge")
	}

	// Crossing the earliest transition (the title bar's refreshed-ago bin at
	// +3s) must rebuild even with no dirty message.
	*clock = m.nextRenderAt
	rebuilt := m.View()
	if m.renderBuilds != builds+1 {
		t.Fatalf("crossing the bin edge did not rebuild: builds %d -> %d", builds, m.renderBuilds)
	}
	if rebuilt.Content == first.Content {
		t.Fatal("rebuild at bin edge produced identical bytes; expected label change")
	}

	// The "done 11m" -> "done 12m" bin edge: jump past it and confirm the
	// rendered label flips. Anchor is start-11m30s, so elapsed is 11m50s at
	// +20s and 12m50s one minute later.
	m.markRenderDirty()
	*clock = start.Add(20 * time.Second)
	mid := m.View()
	if !strings.Contains(mid.Content, "done 11m") {
		t.Fatalf("expected done 11m label before minute bin edge")
	}
	*clock = start.Add(20 * time.Second).Add(time.Minute)
	later := m.View()
	if !strings.Contains(later.Content, "done 12m") {
		t.Fatalf("expected done 12m label after minute bin edge")
	}
}

// TestScheduledDeadlineTickLifecycle verifies the single-flight scheduled
// tick: dirty updates arm a command for the earliest transition, the armed
// generation dirties the frame when it fires, and superseded generations are
// render-neutral.
func TestScheduledDeadlineTickLifecycle(t *testing.T) {
	m, clock := frozenWallModel(t, 3, 0)
	_ = m.View()

	// A dirty message arms a deadline tick (refreshed-ago bin at +3s).
	_, cmd := m.Update(statusMsg("arm"))
	if cmd == nil {
		t.Fatal("dirty update with pending time transitions should return a command batch")
	}
	if m.armedRenderAt.IsZero() || m.nextRenderAt.IsZero() {
		t.Fatalf("deadline not armed: armed=%v next=%v", m.armedRenderAt, m.nextRenderAt)
	}
	_ = m.View()

	gen := m.renderDeadlineGen

	// A stale generation must be ignored.
	builds := m.renderBuilds
	m.Update(renderDeadlineMsg{gen: gen - 1})
	_ = m.View()
	if m.renderBuilds != builds {
		t.Fatal("stale deadline generation dirtied the frame")
	}

	// The armed generation fires at its deadline: dirty, rebuild, re-arm.
	*clock = m.armedRenderAt
	m.Update(renderDeadlineMsg{gen: gen})
	_ = m.View()
	if m.renderBuilds != builds+1 {
		t.Fatal("armed deadline generation did not rebuild the frame")
	}
	if m.renderDeadlineGen == gen && !m.armedRenderAt.IsZero() {
		// Either a new generation was armed for the next transition or none
		// remained; the old generation must not stay armed.
		t.Fatal("deadline tick did not re-arm or clear")
	}
}

// TestPulseSchedulingAndSteadyOnOff proves pulse handling end to end: a
// content change starts the pulse and schedules expiry at exactly
// lastChanged+pulseDuration; the pulsing frame is byte-stable inside the
// window (steady on, no sub-interval animation); crossing expiry rebuilds to
// the non-pulsing frame (steady off).
func TestPulseSchedulingAndSteadyOnOff(t *testing.T) {
	m, clock := frozenWallModel(t, 3, 0)
	m.lastUpdated = time.Time{} // drop the only other time-dependent label
	// Target a Services card: pulse border styling is visible there. (On
	// Active Agents cards the group border color takes precedence and the
	// pulse header color equals the base color, so pulse changes no bytes.)
	target := m.sessions[3]
	if !strings.HasPrefix(target.Name, "svc-go2rtc-") {
		t.Fatalf("fixture order changed: sessions[3] = %s, want a svc-go2rtc session", target.Name)
	}
	paneID := target.Windows[0].Panes[0].ID
	// Services accordions default collapsed; pre-seed the group as expanded so
	// the pulsing card is actually rendered.
	m.seededGroups[groupServices.name] = struct{}{}
	_ = m.View()

	start := *clock
	m.Update(paneContentMsg{generation: m.previews[target.ID].captureGeneration, sessionID: target.ID, paneID: paneID, text: "pulse content"})
	if want := start.Add(pulseDuration); !m.nextRenderAt.Equal(want) {
		t.Fatalf("pulse expiry scheduled at %v, want %v", m.nextRenderAt, want)
	}
	pulsing := m.View()

	// Steady on: mid-window the cached frame is served and a forced rebuild
	// is byte-identical — no animation exists inside the pulse window.
	*clock = start.Add(pulseDuration / 2)
	builds := m.renderBuilds
	midCached := m.View()
	if m.renderBuilds != builds || midCached.Content != pulsing.Content {
		t.Fatal("pulse window should serve the cached pulsing frame")
	}
	m.markRenderDirty()
	midFresh := m.View()
	if midFresh.Content != pulsing.Content {
		t.Fatal("mid-window rebuild differs from pulse-start frame: pulse is not steady on/off")
	}

	// Steady off: crossing expiry rebuilds without the pulse styling.
	*clock = start.Add(pulseDuration)
	expired := m.View()
	if expired.Content == pulsing.Content {
		t.Fatal("pulse expiry did not change the frame")
	}
}

// TestToastExpiresAtScheduledDeadline covers the exact boundary between the
// cache deadline and toast rendering: at exp the frame must rebuild without
// the toast and must not cache a stale toast forever.
func TestToastExpiresAtScheduledDeadline(t *testing.T) {
	m, clock := frozenWallModel(t, 3, 0)
	m.lastUpdated = time.Time{} // keep toast as the only scheduled transition

	m.showToast("boundary toast")
	m.markRenderDirty()
	withToast := m.View()
	if !strings.Contains(withToast.Content, "boundary toast") {
		t.Fatal("expected toast before expiry")
	}
	if m.nextRenderAt.IsZero() || !m.nextRenderAt.Equal(m.toast.exp) {
		t.Fatalf("toast deadline = %v, want %v", m.nextRenderAt, m.toast.exp)
	}

	builds := m.renderBuilds
	*clock = m.toast.exp
	expired := m.View()
	if m.renderBuilds != builds+1 {
		t.Fatalf("exact toast deadline did not rebuild: builds %d -> %d", builds, m.renderBuilds)
	}
	if strings.Contains(expired.Content, "boundary toast") {
		t.Fatal("toast remained visible at exact expiry")
	}
	if !m.nextRenderAt.IsZero() {
		t.Fatalf("expired toast left a future render deadline: %v", m.nextRenderAt)
	}

	again := m.View()
	if m.renderBuilds != builds+1 {
		t.Fatal("expired toast frame should be cached after boundary rebuild")
	}
	if again.Content != expired.Content {
		t.Fatal("cached expired-toast frame changed")
	}
}

// TestZoneHitBoxesStableAcrossCachedFrames asserts the global zone manager
// keeps a non-empty, byte-stable hit-box set while cached frames are served,
// so mouse routing on clean frames uses valid geometry.
func TestZoneHitBoxesStableAcrossCachedFrames(t *testing.T) {
	m, _ := frozenWallModel(t, 12, 4)
	_ = m.View()

	baseline := zone.DefaultManager.ZoneSnapshot()
	if len(baseline) == 0 {
		t.Fatal("fresh render produced no zone hit boxes")
	}
	cardZone := fmt.Sprintf("%scard:%s", m.zonePrefix, "$agent-live-00")
	if info := zone.Get(cardZone); info.IsZero() {
		t.Fatalf("expected card zone %s after fresh render", cardZone)
	}

	for i := 0; i < 30; i++ {
		m.Update(fastTickMsg{})
		_ = m.View()
	}
	after := zone.DefaultManager.ZoneSnapshot()
	if !reflect.DeepEqual(baseline, after) {
		t.Fatal("zone hit boxes changed across clean cached frames")
	}
	if info := zone.Get(cardZone); info.IsZero() {
		t.Fatal("card zone lost during cached frames")
	}
}

// TestJanitorStatusDirtiesExactlyOnRenderedChange covers the file-backed
// derived render input: refreshing from an unchanged file stays clean, a
// changed payload that changes the rendered janitor line dirties the frame,
// and missing/invalid states are render-affecting transitions too.
func TestJanitorStatusDirtiesExactlyOnRenderedChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "janitor-status.json")
	now := time.Now()
	writeStatus := func(kill int) {
		payload := fmt.Sprintf(`{"status_version":1,"generated_at":%q,"last_cycle":{"kill":%d}}`,
			now.Format(time.RFC3339), kill)
		if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
			t.Fatalf("write janitor status: %v", err)
		}
	}

	m, _ := frozenWallModel(t, 3, 0)
	writeStatus(0)
	m.SetJanitorStatusFile(path)
	_ = m.View()

	// Unchanged file: refresh is render-neutral.
	m.refreshJanitorStatus()
	if m.renderDirty {
		t.Fatal("unchanged janitor file dirtied the frame")
	}

	// Changed payload with a changed rendered line: dirty.
	writeStatus(3)
	m.refreshJanitorStatus()
	if !m.renderDirty {
		t.Fatal("changed janitor payload did not dirty the frame")
	}
	before := m.View().Content
	if !strings.Contains(before, "cleanup pending 3") {
		t.Fatalf("janitor line missing from frame")
	}

	// Missing file flips the rendered state: dirty again.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove janitor status: %v", err)
	}
	m.refreshJanitorStatus()
	if !m.renderDirty {
		t.Fatal("missing janitor file did not dirty the frame")
	}
}

// TestClassificationInvalidationDirtiesFrame is the F1/F6 coherence proof:
// classification invalidation can never happen without dirtying the cached
// frame.
func TestClassificationInvalidationDirtiesFrame(t *testing.T) {
	m, _ := frozenWallModel(t, 3, 0)
	_ = m.View()
	if m.renderDirty {
		t.Fatal("expected clean frame after View")
	}
	m.invalidateClassifications()
	if !m.renderDirty {
		t.Fatal("invalidateClassifications did not dirty the frame")
	}
}
