package ui

import (
	"reflect"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestLifecycleRefreshCacheTransitions(t *testing.T) {
	m := benchWallModel(t, 1, 0)
	p := &m.sessions[0].Windows[0].Panes[0]
	steps := []struct {
		name   string
		change func()
	}{
		{"completed", func() { p.PreviewText = "Goal achieved\n› " }},
		{"approval", func() { p.PreviewText += "\n1. approve\n2. reject" }},
		{"active", func() { p.PreviewText = "Thinking… esc to interrupt" }},
		{"auth", func() { p.PreviewText = "device code: ABC\nauthorize" }},
		{"empty", func() { p.PreviewText = "" }},
		{"metadata", func() { p.PreviewText = "new output"; p.Cockpit.State = "done" }},
		{"dead-live", func() { p.Dead = true; p.Cockpit.State = "running" }},
		{"completion-time", func() { p.Cockpit.CompletedAt = "2026-01-01" }},
		{"exit-status", func() { p.DeadStatus = 1 }},
		{"identity", func() { p.Cockpit.Kind = "service"; p.Cockpit.Agent = "" }},
		{"no-metadata", func() { p.Cockpit = nil }},
		{"session-chrome", func() { m.sessions[0].Name = "ordinary"; p.Title = "plain"; p.CurrentCmd = "sh" }},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.change()
			session := m.sessions[0]
			expected := paneLifecycleVerdictFor(*p, sessionHasManagedAgent(session) || containsAny(sessionChromeText(session), agentNameTokens...))
			for i := 0; i < 2; i++ {
				m.refreshLifecycleVerdicts()
				if !reflect.DeepEqual(m.lifecycleVerdicts[p.ID], expected) {
					t.Fatalf("cached=%+v fresh=%+v", m.lifecycleVerdicts[p.ID], expected)
				}
			}
		})
	}
	oldID := p.ID
	m.sessions = nil
	m.refreshLifecycleVerdicts()
	if len(m.lifecycleInputs) != 0 || len(m.lifecycleVerdicts) != 0 {
		t.Fatal("removed panes retained")
	}
	s := agentSessionForGroup("replacement", "running")
	s.Windows[0].Panes[0].ID = oldID
	s.Windows[0].Panes[0].PreviewText = "Goal achieved\n› "
	m.sessions = []tmux.Session{s}
	m.refreshLifecycleVerdicts()
	if m.lifecycleVerdicts[oldID].state != "delivered-idle" {
		t.Fatal("reused pane ID retained old state")
	}
}

func TestLifecycleCaptureAndSnapshotShareCache(t *testing.T) {
	m := benchWallModel(t, 1, 0)
	s := m.sessions[0]
	p := &m.sessions[0].Windows[0].Panes[0]
	p.PreviewText = ""
	for _, content := range []string{"Goal achieved\n› ", "Thinking… esc to interrupt", "1. approve\n2. reject"} {
		m.previews[s.ID].lastContent = content
		m.refreshPaneLifecycleVerdict(s.ID, p.ID, content)
		captured := m.lifecycleVerdicts[p.ID]
		m.refreshLifecycleVerdicts()
		if !reflect.DeepEqual(captured, m.lifecycleVerdicts[p.ID]) {
			t.Fatal("snapshot reverted capture")
		}
		fresh := *p
		fresh.PreviewText = content
		if !reflect.DeepEqual(captured, paneLifecycleVerdictFor(fresh, true)) {
			t.Fatal("stale capture classification")
		}
	}
}

func TestCardBodyCacheExactOutput(t *testing.T) {
	var cache cardBodyCache
	for _, width := range []int{1, 12, 60, 12} {
		for _, body := range []string{"", "ASCII\nnext", "界 e\u0301 👩‍💻\n\x1b[31mred\x1b[0m", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "scrolled\nnew output"} {
			for _, colors := range []bool{true, false} {
				expected := renderCardBodyBlock(width, body, colors)
				for i := 0; i < 2; i++ {
					if got := cache.render(width, body, colors); got != expected {
						t.Fatalf("width=%d body=%q colors=%v", width, body, colors)
					}
				}
			}
		}
	}
}

func TestWarmCardCachesMatchColdFrames(t *testing.T) {
	m, now := frozenWallModel(t, 8, 2)
	_ = m.View()
	steps := []struct {
		name   string
		change func()
	}{
		{"unchanged", func() {}},
		{"narrow", func() { m.width = 85 }},
		{"wide", func() { m.width = 240 }},
		{"focus", func() { m.focusedSession = m.sessions[0].ID }},
		{"cursor", func() { m.cursorSession = m.sessions[1].ID }},
		{"hover", func() { m.hoveredSession = m.sessions[0].ID }},
		{"collapsed", func() { m.collapsed[m.sessions[0].ID] = struct{}{} }},
		{"expanded", func() { delete(m.collapsed, m.sessions[0].ID) }},
		{"time", func() { *now = now.Add(70 * time.Second) }},
		{"output", func() {
			for _, p := range m.previews {
				p.viewport.SetContent("界 e\u0301 👩‍💻\n\x1b[31mstream\x1b[0m\nnew line")
			}
		}},
		{"scroll", func() {
			for _, p := range m.previews {
				p.viewport.GotoTop()
			}
		}},
		{"terminal", func() {
			m.sessions[0].Windows[0].Panes[0].PreviewText = "Goal achieved\n› "
			m.refreshLifecycleVerdicts()
			m.invalidateClassifications()
		}},
		{"approval", func() {
			m.sessions[0].Windows[0].Panes[0].PreviewText = "1. approve\n2. reject"
			m.refreshLifecycleVerdicts()
			m.invalidateClassifications()
		}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.change()
			m.markRenderDirty()
			warm := m.View().Content
			zones := dumpZones()
			layout := append([]cardBounds(nil), m.cardLayout...)
			for _, p := range m.previews {
				p.bodyCache = cardBodyCache{}
				p.cardCache = cardCompositionCache{}
			}
			m.markRenderDirty()
			cold := m.View().Content
			if warm != cold {
				t.Fatal("warm frame differs from cold frame")
			}
			if zones != dumpZones() || !reflect.DeepEqual(layout, m.cardLayout) {
				t.Fatal("cache changed hitboxes")
			}
		})
	}
}
