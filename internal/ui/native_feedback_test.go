package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// Exercise the complete feedback path with a real resize-aware terminal.
// No provider/network is needed: only the external TUI's screen is simulated.
func TestNativeFitLifecycleFeedback(t *testing.T) {
	bin := disposableTmuxWrapper(t)
	dir := t.TempDir()
	modePath := filepath.Join(dir, "mode")
	writeMode := func(mode string) {
		t.Helper()
		// The terminal reads concurrently. Publish a complete mode atomically;
		// truncating the live file can crash the fixture on an empty mode.
		if err := os.WriteFile(modePath+".next", []byte(mode), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(modePath+".next", modePath); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "screen.py")
	source, err := os.ReadFile("testdata/native_feedback.py")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, source, 0o600); err != nil {
		t.Fatal(err)
	}
	writeMode("idle")
	tmuxOut(t, bin, "new-session", "-d", "-s", "feedback", "-x", "160", "-y", "45", "python3 "+script+" "+modePath)
	c, err := tmux.NewClient(bin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	n := tmux.NewNativeSizer(c)
	defer func() {
		if err := n.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	m := modelForAccordionSizing(360, 24)
	m.SetNativeSizer(n)
	m.setAllGroupsCollapsed(allCockpitGroups(), false)
	originalPID := ""
	for _, step := range []struct{ mode, state string }{
		{"idle", "delivered-idle"},
		{"working", "live-working"},
		{"permission", "awaiting-operator"},
		{"background", "live-working"},
		{"idle", "delivered-idle"},
	} {
		writeMode(step.mode)
		for cycle := 0; cycle < 12; cycle++ {
			sizes := [][2]int{{360, 24}, {140, 50}, {360, 87}}
			size := sizes[cycle/4]
			m.width, m.height = size[0], size[1]
			snap, err := c.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			m.sessions = snap.Sessions
			s := &m.sessions[0]
			p := &s.Windows[0].Panes[0]
			if originalPID == "" {
				originalPID = p.PID
			}
			if p.PID != originalPID {
				t.Fatal("worker replaced")
			}
			marker := fmt.Sprintf("FRAME-%dx%d-%s", p.Width, p.Height, step.mode)
			content := ""
			for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
				content, err = c.CapturePane(ctx, p.ID, 200)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(content, marker) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !strings.Contains(content, marker) {
				t.Fatalf("no settled frame %s: %q", marker, content)
			}
			p.Cockpit = &tmux.CockpitMeta{Kind: "visible-agent", Agent: "claude", State: "waiting", ContractVersion: "1", ManagedBy: "agent_wall"}
			p.PreviewText = content
			seedPreviewForSizingTest(m, *s, p.Width, p.Height)
			m.previews[s.ID].lastContent = content
			m.previews[s.ID].viewport.SetContent(content)
			m.sessions = append(m.sessions, sessionForGroup("service", "python", "/workspace", "Service"))
			m.refreshLifecycleVerdicts()
			m.invalidateClassifications()
			if got := m.cachedLifecycleVerdict(*p, *s).state; got != step.state {
				t.Fatalf("%s cycle %d size %dx%d: state=%s want=%s", step.mode, cycle, p.Width, p.Height, got, step.state)
			}
			wantGroup := groupActiveAgents.name
			if step.mode == "idle" {
				wantGroup = groupCompletedAgents.name
			}
			if got := cockpitGroupFor(m, *s).name; got != wantGroup {
				t.Fatalf("group=%s want=%s", got, wantGroup)
			}
			m.renderSessionPreviews(m.previewOffset)
			if err := n.Sync(ctx, m.nativeTargets); err != nil {
				t.Fatal(err)
			}
			var width, height int
			got := tmuxOut(t, bin, "display-message", "-p", "-t", "feedback", "#{pane_width} #{pane_height}")
			if _, err := fmt.Sscan(got, &width, &height); err != nil {
				t.Fatal(err)
			}
			if width < 160 || height < 45 {
				t.Fatalf("native terminal crushed to %dx%d", width, height)
			}
		}
	}
	if err := n.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tmuxOut(t, bin, "display-message", "-p", "-t", "feedback", "#{pane_width}x#{pane_height}")); got != "160x45" {
		t.Fatal(got)
	}
}

func TestCaptureFailureIsNotWorkerActivity(t *testing.T) {
	m := benchWallModel(t, 1, 0)
	s := m.sessions[0]
	p := &m.sessions[0].Windows[0].Panes[0]
	p.PreviewText = ""
	preview := m.previews[s.ID]
	preview.lastContent = "Cooked for 7m 2s\n❯ "
	m.refreshLifecycleVerdicts()
	if got := m.cachedLifecycleVerdict(*p, s).state; got != "delivered-idle" {
		t.Fatal(got)
	}
	m.Update(paneContentMsg{sessionID: s.ID, paneID: p.ID, generation: preview.captureGeneration, err: fmt.Errorf("temporary tmux transport failure")})
	m.refreshLifecycleVerdicts()
	if got := m.cachedLifecycleVerdict(*p, s).state; got != "delivered-idle" {
		t.Fatalf("capture error changed worker state: %s", got)
	}
	if strings.Contains(preview.lastContent, "error") {
		t.Fatal("transport diagnostic became worker output")
	}
	m.Update(paneContentMsg{sessionID: s.ID, paneID: p.ID, generation: preview.captureGeneration, text: "Thinking… esc to interrupt"})
	if got := m.cachedLifecycleVerdict(*p, s).state; got != "live-working" {
		t.Fatalf("new activity not detected: %s", got)
	}
}
