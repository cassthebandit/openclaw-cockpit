package ui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCaptureAndNativeSizeToastInvalidateFrame(t *testing.T) {
	for _, source := range []string{"capture", "native-size"} {
		t.Run(source, func(t *testing.T) {
			m, _ := frozenWallModel(t, 1, 0)
			_ = m.View()
			builds := m.renderBuilds
			failure := errors.New("synthetic transport failure")
			if source == "capture" {
				session := m.sessions[0]
				preview := m.previews[session.ID]
				before := preview.lastContent
				m.Update(paneContentMsg{sessionID: session.ID, paneID: preview.paneID, generation: preview.captureGeneration, err: failure})
				if preview.lastContent != before {
					t.Fatal("failed acquisition replaced content")
				}
			} else {
				m.Update(nativeSizeMsg{err: failure})
			}
			view := m.View()
			if !strings.Contains(view.Content, failure.Error()) || m.renderBuilds != builds+1 {
				t.Fatalf("next View did not show toast with one rebuild: builds %d -> %d", builds, m.renderBuilds)
			}
		})
	}
}

func TestSuccessfulNativeSizeDoesNotRebuildFrame(t *testing.T) {
	m, _ := frozenWallModel(t, 1, 0)
	_ = m.View()
	builds := m.renderBuilds
	for range 10 {
		m.nativeSizing = true
		m.Update(nativeSizeMsg{})
		_ = m.View()
		if m.nativeSizing || m.renderBuilds != builds {
			t.Fatal("successful native fit did not clear bookkeeping without rebuilding")
		}
	}
}

func TestSnapshotConsumesSignalWithoutDelayingStaleRefresh(t *testing.T) {
	for _, signal := range []string{"valid", "absent", "missing-file"} {
		t.Run(signal, func(t *testing.T) {
			now := time.Unix(1752000000, 0)
			m := NewModel(nil, time.Second, 4, nil, false, true)
			m.SetOrganized(true)
			m.clock = func() time.Time { return now }
			paths := buildBudgetWall(t, m, t.TempDir(), 1)
			session := &m.sessions[0]
			switch signal {
			case "absent":
				session.Windows[0].Panes[0].Cockpit.PaneLog = ""
			case "missing-file":
				if err := os.Remove(paths[0]); err != nil {
					t.Fatal(err)
				}
			}
			m.ensurePreviewsAndCapture()
			if len(m.fastCaptureActive) != 1 {
				t.Fatal("initial snapshot must capture")
			}
			delete(m.fastCaptureActive, session.ID)
			if got := m.planFastCaptures(); len(got) != 0 {
				t.Fatalf("fast path duplicated snapshot observation: %+v", got)
			}
			if signal == "valid" {
				// A frozen valid log must not suppress snapshot acquisition:
				// output can still change without any log signal.
				now = now.Add(time.Second)
				m.ensurePreviewsAndCapture()
				if len(m.fastCaptureActive) != 1 {
					t.Fatal("stale valid signal delayed existing snapshot refresh bound")
				}
			} else {
				now = now.Add(fastCaptureFallback - time.Nanosecond)
				m.ensurePreviewsAndCapture()
				if len(m.fastCaptureActive) != 0 {
					t.Fatal("snapshot duplicated fallback inside 250ms bound")
				}
				now = now.Add(time.Nanosecond)
				if got := m.planFastCaptures(); len(got) != 1 {
					t.Fatal("no-signal fallback did not become eligible at 250ms")
				}
			}
		})
	}
}

func TestSnapshotSignalBudgetRefusalDoesNotSwallowChange(t *testing.T) {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	buildBudgetWall(t, m, t.TempDir(), 1)
	now := time.Unix(1752000000, 0)
	m.clock = func() time.Time { return now }
	for range m.effectiveCaptureRate() {
		m.takeCaptureToken(now)
	}
	m.ensurePreviewsAndCapture()
	if m.previews[m.sessions[0].ID].signal.seen || len(m.fastCaptureActive) != 0 {
		t.Fatal("denied capture consumed the output signal")
	}
	now = now.Add(time.Second)
	if got := m.planFastCaptures(); len(got) != 1 {
		t.Fatal("denied signal was lost after budget refill")
	}
}

func TestFocusedAndDetailSnapshotKeepImmediateAcquisition(t *testing.T) {
	for _, detail := range []bool{false, true} {
		m := NewModel(nil, time.Second, 4, nil, false, true)
		m.SetOrganized(true)
		buildBudgetWall(t, m, t.TempDir(), 1)
		session := &m.sessions[0]
		session.Windows[0].Panes[0].Cockpit.PaneLog = ""
		if detail {
			m.viewMode, m.detailSession = viewModeDetail, session.ID
		} else {
			m.focusedSession = session.ID
		}
		m.ensurePreviewsAndCapture()
		delete(m.fastCaptureActive, session.ID)
		m.ensurePreviewsAndCapture()
		if len(m.fastCaptureActive) != 1 {
			t.Fatal("focused/detail snapshot was throttled")
		}
	}
}
