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

// A real alternate-screen program draws one numbered line per terminal row.
// This checks additional useful content, not just resize command success.
func TestNativeFitRendersAdditionalRows(t *testing.T) {
	bin := disposableTmuxWrapper(t)
	script := filepath.Join(t.TempDir(), "screen.py")
	source := `import os, signal, sys, time
sys.stdout.write("\033[?1049h")
def draw(*args):
    w,h=os.get_terminal_size()
    sys.stdout.write("\033[H\033[2J")
    for i in range(1,h+1):
        sys.stdout.write("\033[%d;1HROW-%03d" % (i,i))
    sys.stdout.flush()
signal.signal(signal.SIGWINCH,draw)
draw()
while True: time.sleep(0.1)
`
	if err := os.WriteFile(script, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	tmuxOut(t, bin, "new-session", "-d", "-s", "native-render", "-x", "160", "-y", "45", "python3 "+script)
	c, err := tmux.NewClient(bin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var snap tmux.Snapshot
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		snap, err = c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Sessions[0].Windows[0].Panes[0].AlternateScreen {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	m := modelForAccordionSizing(300, 87)
	n := tmux.NewNativeSizer(c)
	m.SetNativeSizer(n)
	defer func() {
		if err := n.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	for _, size := range [][2]int{{300, 87}, {150, 50}, {300, 95}} {
		m.width, m.height = size[0], size[1]
		snap, err = c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m.sessions = snap.Sessions
		s := &m.sessions[0]
		p := &s.Windows[0].Panes[0]
		p.Cockpit = &tmux.CockpitMeta{Kind: "visible-agent", Agent: "claude", State: "running", ContractVersion: "1", ManagedBy: "agent_wall"}
		seedPreviewForSizingTest(m, *s, 160, 45)
		m.renderSessionPreviews(m.previewOffset)
		if len(m.nativeTargets) != 1 {
			t.Fatalf("targets: %+v", m.nativeTargets)
		}
		target := m.nativeTargets[0]
		if size[1] >= 87 && target.Height <= 45 {
			t.Fatalf("target still capped: %+v", target)
		}
		msg, ok := m.syncNativeSizeCmd()().(nativeSizeMsg)
		if !ok {
			t.Fatal("unexpected sizing result")
		}
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		m.nativeSizing = false
		marker := fmt.Sprintf("ROW-%03d", target.Height)
		var content string
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			content, err = c.CapturePane(ctx, p.ID, 200)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(content, marker) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		p.Width, p.Height = target.Width, target.Height
		preview := m.previews[s.ID]
		preview.viewport.SetContent(strings.TrimRight(content, "\n"))
		preview.lastContent = content
		rendered := m.renderSessionPreviews(m.previewOffset)
		if !strings.Contains(rendered, marker) {
			t.Fatalf("missing real terminal row %s at %v", marker, size)
		}
		t.Logf("dashboard=%v native=%dx%d visible=%s", size, target.Width, target.Height, marker)
	}
	m.collapsedGroups[groupActiveAgents.name] = struct{}{}
	m.renderSessionPreviews(m.previewOffset)
	if len(m.nativeTargets) != 0 {
		t.Fatal("collapsed group retained fit")
	}
	if err := n.Sync(ctx, m.nativeTargets); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tmuxOut(t, bin, "display-message", "-p", "-t", "native-render", "#{pane_width}x#{pane_height}")); got != "160x45" {
		t.Fatal(got)
	}
}

func TestNativeFitUsesEveryExpandedCardGeometry(t *testing.T) {
	for _, count := range []int{1, 2, 5} {
		m := modelForAccordionSizing(300, 87)
		m.SetNativeSizer(tmux.NewNativeSizer(nil)) // planning only; never dispatch
		for i := 0; i < count; i++ {
			s := sessionForGroup(fmt.Sprintf("native-%d", i), "claude", "/workspace", "Work")
			s.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{Kind: "visible-agent", Agent: "claude", State: "running", ContractVersion: "1", ManagedBy: "agent_wall"}
			s.Windows[0].Panes[0].Height = 45
			s.Windows[0].Panes[0].AlternateScreen = true
			m.sessions = append(m.sessions, s)
			seedPreviewForSizingTest(m, s, 160, 45)
		}
		m.renderSessionPreviews(m.previewOffset)
		if len(m.nativeTargets) != count {
			t.Fatalf("%d cards produced %d targets", count, len(m.nativeTargets))
		}
		for _, target := range m.nativeTargets {
			if target.Width < 20 || target.Height < 3 {
				t.Fatalf("invalid allocation: %+v", target)
			}
		}
		m.sessions[0].Attached = true
		m.renderSessionPreviews(m.previewOffset)
		if len(m.nativeTargets) != count-1 {
			t.Fatal("attached card remained a target")
		}
	}
}
