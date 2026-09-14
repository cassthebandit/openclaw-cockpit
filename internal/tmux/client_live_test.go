package tmux

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/testutil"
)

func disposableTmux(t *testing.T) string {
	t.Helper()
	return testutil.TmuxWrapper(t)
}

func runDisposable(t *testing.T, wrapper string, args ...string) string {
	t.Helper()
	return testutil.TmuxOut(t, wrapper, args...)
}

// TestLiteralVersusNamedKeySemanticsOnRealTmux proves AC5 on a real
// disposable tmux server: printable text is forwarded with literal semantics
// (typing the characters "C-c" does not interrupt the pane process), while
// the named C-c token delivers a genuine interrupt.
func TestLiteralVersusNamedKeySemanticsOnRealTmux(t *testing.T) {
	t.Parallel()

	wrapper := disposableTmux(t)
	runDisposable(t, wrapper, "new-session", "-d", "-s", "ctl", "-x", "80", "-y", "24", "cat")
	runDisposable(t, wrapper, "set-option", "-t", "ctl", "remain-on-exit", "on")

	client := &Client{bin: wrapper}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paneID := strings.TrimSpace(runDisposable(t, wrapper, "display-message", "-p", "-t", "ctl", "#{pane_id}"))

	// Literal path: the text "C-c; echo Enter" must arrive as characters.
	if err := client.SendLiteralKeys(ctx, paneID, "C-c; echo Enter"); err != nil {
		t.Fatalf("SendLiteralKeys: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	content := runDisposable(t, wrapper, "capture-pane", "-p", "-t", paneID)
	if !strings.Contains(content, "C-c; echo Enter") {
		t.Fatalf("literal text did not round-trip, pane content:\n%s", content)
	}
	if dead := strings.TrimSpace(runDisposable(t, wrapper, "display-message", "-p", "-t", paneID, "#{pane_dead}")); dead != "0" {
		t.Fatalf("literal \"C-c\" text must not interrupt the pane, pane_dead=%s", dead)
	}

	// Named-token path: the C-c key token must deliver a real interrupt.
	if err := client.SendKeys(ctx, paneID, "C-c"); err != nil {
		t.Fatalf("SendKeys C-c: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		dead := strings.TrimSpace(runDisposable(t, wrapper, "display-message", "-p", "-t", paneID, "#{pane_dead}"))
		if dead == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("named C-c token did not interrupt the pane, pane_dead=%s", dead)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestNamedEnterStaysAKeyTokenOnRealTmux proves Enter forwards as a named key:
// a shell command typed literally executes only after the Enter token.
func TestNamedEnterStaysAKeyTokenOnRealTmux(t *testing.T) {
	t.Parallel()

	wrapper := disposableTmux(t)
	runDisposable(t, wrapper, "new-session", "-d", "-s", "ent", "-x", "80", "-y", "24", "/bin/sh")

	client := &Client{bin: wrapper}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paneID := strings.TrimSpace(runDisposable(t, wrapper, "display-message", "-p", "-t", "ent", "#{pane_id}"))
	if err := client.SendLiteralKeys(ctx, paneID, "echo OC_MARKER_$((20+22))"); err != nil {
		t.Fatalf("SendLiteralKeys: %v", err)
	}
	if err := client.SendKeys(ctx, paneID, "Enter"); err != nil {
		t.Fatalf("SendKeys Enter: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		content := runDisposable(t, wrapper, "capture-pane", "-p", "-t", paneID)
		if strings.Contains(content, "OC_MARKER_42") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Enter token did not execute the literal command, pane content:\n%s", content)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSnapshotLinkedWindowContext(t *testing.T) {
	wrapper := disposableTmux(t)
	runDisposable(t, wrapper, "new-session", "-d", "-s", "alpha", "cat")
	runDisposable(t, wrapper, "new-session", "-d", "-s", "beta", "cat")
	runDisposable(t, wrapper, "link-window", "-s", "alpha:0", "-t", "beta:1")
	for _, excluded := range []string{"", "alpha", "beta"} {
		client := &Client{bin: wrapper, excludeSessions: map[string]bool{excluded: true}}
		snapshot, err := client.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range snapshot.Sessions {
			for _, window := range session.Windows {
				if len(window.Panes) != 1 {
					t.Fatalf("excluded=%q session=%s window=%s panes=%d", excluded, session.Name, window.ID, len(window.Panes))
				}
				if window.Panes[0].Session != session.ID {
					t.Fatalf("pane attached to wrong session: %+v", window.Panes[0])
				}
			}
		}
	}
}
