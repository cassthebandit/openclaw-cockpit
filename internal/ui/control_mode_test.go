package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// disposableTmuxWrapper starts a private tmux server on a unique -L socket
// and returns a wrapper binary pinning tmux invocations to it. The default
// tmux server is never touched.
func disposableTmuxWrapper(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed; disposable-server test skipped")
	}
	socket := fmt.Sprintf("oc-cockpit-ui-test-%d", os.Getpid())
	wrapper := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\nexec " + real + " -L " + socket + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write tmux wrapper: %v", err)
	}
	t.Cleanup(func() {
		// An already-exited server is the normal case, so this must never fail
		// the test; log it so an abnormal failure that leaks a private tmux
		// server past the run is visible instead of silent.
		if err := exec.Command(real, "-L", socket, "kill-server").Run(); err != nil {
			t.Logf("kill-server on socket %s: %v", socket, err)
		}
	})
	return wrapper
}

func tmuxOut(t *testing.T, wrapper string, args ...string) string {
	t.Helper()
	out, err := exec.Command(wrapper, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestDoubleCtrlCForwardsExactlyOneInterrupt proves AC4 against a real
// disposable tmux server through the real Model/Client code path: the first
// Ctrl+C forwards exactly one C-c to the focused live pane and arms the quit
// chord; the second press inside the window quits Cockpit without forwarding
// a second interrupt.
func TestDoubleCtrlCForwardsExactlyOneInterrupt(t *testing.T) {
	wrapper := disposableTmuxWrapper(t)
	tmuxOut(t, wrapper, "new-session", "-d", "-s", "chord", "-x", "80", "-y", "24",
		"/bin/sh", "-c", `trap 'echo GOT-INT' INT; while :; do sleep 0.1; done`)

	client, err := tmux.NewClient(wrapper)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 1 || len(snap.Sessions[0].Windows) == 0 || len(snap.Sessions[0].Windows[0].Panes) == 0 {
		t.Fatalf("unexpected disposable snapshot shape: %+v", snap.Sessions)
	}
	session := snap.Sessions[0]
	pane := session.Windows[0].Panes[0]

	m := NewModel(client, time.Second, 4, nil, false, false)
	m.sessions = snap.Sessions
	m.focusedSession = session.ID
	vp := viewportFor(innerDimension{width: 60, height: 20})
	m.previews[session.ID] = &sessionPreview{viewport: &vp, paneID: pane.ID, autoFollow: true}

	press := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

	handled, cmd1 := m.handleFocusedKey(press)
	if !handled || cmd1 == nil {
		t.Fatalf("first ctrl+c: handled=%v cmd=%v, want forwarded C-c", handled, cmd1)
	}
	// Second press arrives inside the chord window, before any capture waits.
	handled, cmd2 := m.handleFocusedKey(press)
	if !handled || cmd2 == nil {
		t.Fatalf("second ctrl+c: handled=%v cmd=%v", handled, cmd2)
	}
	if _, ok := cmd2().(tea.QuitMsg); !ok {
		t.Fatalf("second ctrl+c must quit without forwarding, got %#v", cmd2())
	}

	// Execute the first press's forward against the real pane.
	if msg := cmd1(); msg != nil {
		t.Fatalf("forwarding C-c failed: %#v", msg)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		content := tmuxOut(t, wrapper, "capture-pane", "-p", "-t", pane.ID)
		got := strings.Count(content, "GOT-INT")
		if got == 1 && time.Now().After(deadline.Add(-2*time.Second)) {
			// Interrupt observed; hold briefly to catch a stray second one.
			time.Sleep(300 * time.Millisecond)
			content = tmuxOut(t, wrapper, "capture-pane", "-p", "-t", pane.ID)
			if final := strings.Count(content, "GOT-INT"); final != 1 {
				t.Fatalf("expected exactly one interrupt, pane saw %d:\n%s", final, content)
			}
			return
		}
		if got > 1 {
			t.Fatalf("expected exactly one interrupt, pane saw %d:\n%s", got, content)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never observed the forwarded interrupt:\n%s", content)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestMonitorOnlyBlocksNamedKeyForwarding pins AC5's fail-closed side for
// named special keys (the printable path is covered by
// TestMonitorOnlyBlocksKeyForwarding).
func TestMonitorOnlyBlocksNamedKeyForwarding(t *testing.T) {
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

	handled, cmd := m.handleFocusedKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || cmd == nil {
		t.Fatalf("Enter in monitor-only: handled=%v cmd=%v", handled, cmd)
	}
	if got, ok := cmd().(statusMsg); !ok || !strings.Contains(string(got), "monitor-only") {
		t.Fatalf("expected monitor-only refusal status, got %#v", cmd())
	}
}

// TestTmuxKeysFromSeparatesLiteralAndNamed pins the AC5 forwarding grammar.
func TestTmuxKeysFromSeparatesLiteralAndNamed(t *testing.T) {
	t.Parallel()

	named := map[string]tea.KeyPressMsg{
		"Enter":  {Code: tea.KeyEnter},
		"Tab":    {Code: tea.KeyTab},
		"BSpace": {Code: tea.KeyBackspace},
		"Delete": {Code: tea.KeyDelete},
		"Escape": {Code: tea.KeyEsc},
		"Up":     {Code: tea.KeyUp},
		"Down":   {Code: tea.KeyDown},
		"Left":   {Code: tea.KeyLeft},
		"Right":  {Code: tea.KeyRight},
	}
	for token, msg := range named {
		input, ok := tmuxKeysFrom(msg)
		if !ok || input.literal || len(input.keys) != 1 || input.keys[0] != token {
			t.Fatalf("tmuxKeysFrom(%s) = %+v ok=%v, want named token %q", token, input, ok, token)
		}
	}

	for _, text := range []string{"a", ";", "-", "C", " "} {
		msg := tea.KeyPressMsg{Text: text, Code: rune(text[0])}
		if text == " " {
			msg = tea.KeyPressMsg{Code: tea.KeySpace}
		}
		input, ok := tmuxKeysFrom(msg)
		if !ok || !input.literal || input.text != text {
			t.Fatalf("tmuxKeysFrom(%q) = %+v ok=%v, want literal text", text, input, ok)
		}
	}
}

// Tab navigation must change the terminal that receives actual client commands,
// not merely the title rendered above a stale focused preview.
func TestDetailTabKeyboardForwardsToDisplayedPane(t *testing.T) {
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("COCKPIT_TEST_TMUX_LOG", log)
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$COCKPIT_TEST_TMUX_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	client, err := tmux.NewClient(binary)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(client, time.Second, 4, nil, false, false)
	m.width, m.height = 120, 40
	for i, id := range []string{"$1", "$2"} {
		paneID := fmt.Sprintf("%%%d", i+101)
		m.sessions = append(m.sessions, tmux.Session{
			ID: id, Name: id,
			Windows: []tmux.Window{{Active: true, Panes: []tmux.Pane{{ID: paneID, Active: true}}}},
		})
		vp := viewportFor(innerDimension{width: 60, height: 20})
		m.previews[id] = &sessionPreview{viewport: &vp, paneID: paneID, autoFollow: true}
	}
	m.enterDetail("$1")
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	if m.detailSession != "$2" {
		t.Fatalf("keyboard navigation did not display second session: %q", m.detailSession)
	}
	for _, key := range []tea.KeyPressMsg{{Code: 'a', Text: "a"}, {Code: 'c', Mod: tea.ModCtrl}} {
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatalf("key %q did not produce a forwarding command", key.String())
		}
		if msg := cmd(); msg != nil {
			t.Fatalf("key %q forwarding returned %#v", key.String(), msg)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "send-keys -l -t %102 -- a\nsend-keys -t %102 -- C-c\n"
	if string(data) != want {
		t.Fatalf("wrong terminal received input:\ngot %q\nwant %q", data, want)
	}
}
