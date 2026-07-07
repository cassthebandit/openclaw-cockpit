package tmux

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestNewClientMissingTmux(t *testing.T) {
	t.Parallel()

	_, err := newClient("", func(string) (string, error) {
		return "", exec.ErrNotFound
	})
	if err == nil {
		t.Fatal("expected error when tmux is missing")
	}
	if !strings.Contains(err.Error(), "install tmux") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListSessionsNoServer(t *testing.T) {
	t.Parallel()

	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("failed to connect to server")}
	}}

	sessions, err := c.listSessions(context.Background())
	if err != nil {
		t.Fatalf("listSessions returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions, got %d", len(sessions))
	}
}

func TestListWindowsNoServer(t *testing.T) {
	t.Parallel()

	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("no server running")}
	}}

	windows, err := c.listWindows(context.Background())
	if err != nil {
		t.Fatalf("listWindows returned error: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("expected no windows, got %d", len(windows))
	}
}

func TestListPanesNoServer(t *testing.T) {
	t.Parallel()

	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("failed to connect to server")}
	}}

	panes, skipped, err := c.listPanes(context.Background())
	if err != nil {
		t.Fatalf("listPanes returned error: %v", err)
	}
	if len(panes) != 0 {
		t.Fatalf("expected no panes, got %d", len(panes))
	}
	if skipped != 0 {
		t.Fatalf("skipped panes = %d, want 0", skipped)
	}
}

func TestListPanesParsesCockpitMetadata(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{
		"$1",
		"@1",
		"%1",
		"1",
		"zsh",
		"Cass worker",
		"100",
		"90",
		"120",
		"30",
		"/dev/ttys001",
		"/tmp/project",
		"0",
		"",
		"1",
		"agent_wall",
		"agent",
		"codex",
		"workshop-4",
		"tmuxwatch",
		"Build monitor-only mode",
		"waiting",
		"memory/runs/tmuxwatch",
		"thread-1",
		"session-1",
		"2026-07-02T05:00:00Z",
		"2026-07-02T05:01:00Z",
		"2026-07-02T05:02:00Z",
		"0",
		"30m",
		"kill_on_done",
		"result.txt",
		"review hold",
	}, tmuxFieldSep) + "\n"

	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(line), nil
	}}

	panes, skipped, err := c.listPanes(context.Background())
	if err != nil {
		t.Fatalf("listPanes returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected one pane, got %d", len(panes))
	}
	if skipped != 0 {
		t.Fatalf("skipped panes = %d, want 0", skipped)
	}
	meta := panes[0].Cockpit
	if meta == nil {
		t.Fatal("expected cockpit metadata")
	}
	if meta.Agent != "codex" || meta.Owner != "workshop-4" || meta.State != "waiting" {
		t.Fatalf("unexpected cockpit metadata: %+v", meta)
	}
	if meta.ManagedBy != "agent_wall" || meta.CleanupPolicy != "kill_on_done" || meta.EvidencePath != "result.txt" {
		t.Fatalf("unexpected cockpit cleanup metadata: %+v", meta)
	}
	if panes[0].CurrentPath != "/tmp/project" {
		t.Fatalf("CurrentPath = %q, want /tmp/project", panes[0].CurrentPath)
	}
}

func TestSnapshotExcludesNamedSessions(t *testing.T) {
	t.Parallel()

	c := &Client{bin: "tmux", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "list-sessions":
			return []byte(strings.Join([]string{
				strings.Join([]string{"$1", "cass-agents", "1", "100", "110"}, tmuxFieldSep),
				strings.Join([]string{"$2", "camera-rtsp", "0", "100", "120"}, tmuxFieldSep),
			}, "\n") + "\n"), nil
		case "list-windows":
			return []byte(strings.Join([]string{
				strings.Join([]string{"$1", "@1", "0", "dashboard", "1", "0"}, tmuxFieldSep),
				strings.Join([]string{"$2", "@2", "0", "go2rtc", "1", "0"}, tmuxFieldSep),
			}, "\n") + "\n"), nil
		case "list-panes":
			return []byte(strings.Join([]string{
				strings.Join([]string{"$1", "@1", "%1", "1", "tmuxwatch-cass", "", "110", "100", "80", "24", "/dev/ttys001", "/tmp", "0", ""}, tmuxFieldSep),
				strings.Join([]string{"$2", "@2", "%2", "1", "go2rtc", "", "120", "100", "80", "24", "/dev/ttys002", "/tmp", "0", ""}, tmuxFieldSep),
			}, "\n") + "\n"), nil
		default:
			t.Fatalf("unexpected tmux command: %#v", args)
			return nil, nil
		}
	}}
	c.SetExcludedSessions([]string{"cass-agents"})

	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("expected one visible session, got %d", len(snap.Sessions))
	}
	if snap.Sessions[0].Name != "camera-rtsp" {
		t.Fatalf("visible session = %q, want camera-rtsp", snap.Sessions[0].Name)
	}
	if len(snap.Sessions[0].Windows) != 1 || len(snap.Sessions[0].Windows[0].Panes) != 1 {
		t.Fatalf("expected visible session to keep joined windows/panes: %+v", snap.Sessions[0])
	}
}

func TestListSessionsPreservesUnderscoreHeavyNames(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{"$179", "AI-Alerts_with_under_score", "0", "1783018270", "1783018270"}, tmuxFieldSep) + "\n"
	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(line), nil
	}}

	sessions, err := c.listSessions(context.Background())
	if err != nil {
		t.Fatalf("listSessions returned error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(sessions))
	}
	if sessions[0].Name != "AI-Alerts_with_under_score" {
		t.Fatalf("Name = %q", sessions[0].Name)
	}
}

func TestListPanesSkipsMalformedRows(t *testing.T) {
	t.Parallel()

	good := strings.Join([]string{"$1", "@1", "%1", "1", "zsh", "title", "110", "100", "80", "24", "/dev/ttys001", "/tmp", "0", ""}, tmuxFieldSep)
	bad := strings.Join([]string{"$1", "@1", "%bad", "1", "zsh", "title", "not-a-time", "100", "80", "24", "/dev/ttys001", "/tmp", "0", ""}, tmuxFieldSep)
	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(bad + "\n" + "too\tfew\tfields\n" + good + "\n"), nil
	}}

	panes, skipped, err := c.listPanes(context.Background())
	if err != nil {
		t.Fatalf("listPanes returned error: %v", err)
	}
	if len(panes) != 1 || panes[0].ID != "%1" {
		t.Fatalf("expected one good pane, got %+v", panes)
	}
	if skipped != 2 {
		t.Fatalf("skipped panes = %d, want 2", skipped)
	}
}

func TestSnapshotCarriesMalformedPaneWarningCount(t *testing.T) {
	t.Parallel()

	c := &Client{bin: "tmux", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "list-sessions":
			return []byte(strings.Join([]string{"$1", "worker", "0", "100", "120"}, tmuxFieldSep) + "\n"), nil
		case "list-windows":
			return []byte(strings.Join([]string{"$1", "@1", "0", "main", "1", "0"}, tmuxFieldSep) + "\n"), nil
		case "list-panes":
			good := strings.Join([]string{"$1", "@1", "%1", "1", "zsh", "title", "110", "100", "80", "24", "/dev/ttys001", "/tmp", "0", ""}, tmuxFieldSep)
			bad := strings.Join([]string{"$1", "@1", "%bad", "1", "zsh", "title", "not-a-time", "100", "80", "24", "/dev/ttys001", "/tmp", "0", ""}, tmuxFieldSep)
			return []byte(bad + "\n" + good + "\n"), nil
		default:
			t.Fatalf("unexpected tmux command: %#v", args)
			return nil, nil
		}
	}}

	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if snap.PaneParseWarnings != 1 {
		t.Fatalf("PaneParseWarnings = %d, want 1", snap.PaneParseWarnings)
	}
}

func TestListPanesPreservesTabsInFields(t *testing.T) {
	t.Parallel()

	line := strings.Join([]string{"$1", "@1", "%1", "1", "zsh", "title\twith\ttabs", "110", "100", "80", "24", "/dev/ttys001", "/tmp/path\twith\ttabs", "0", ""}, tmuxFieldSep) + "\n"
	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(line), nil
	}}

	panes, skipped, err := c.listPanes(context.Background())
	if err != nil {
		t.Fatalf("listPanes returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected one pane, got %d", len(panes))
	}
	if skipped != 0 {
		t.Fatalf("skipped panes = %d, want 0", skipped)
	}
	if panes[0].Title != "title\twith\ttabs" || panes[0].CurrentPath != "/tmp/path\twith\ttabs" {
		t.Fatalf("tabs were not preserved: %+v", panes[0])
	}
}

func TestCapturePaneUsesPlainTextByDefault(t *testing.T) {
	t.Parallel()

	var gotArgs []string
	c := &Client{bin: "tmux", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("plain\n"), nil
	}}

	out, err := c.CapturePane(context.Background(), "%1", 40)
	if err != nil {
		t.Fatalf("CapturePane returned error: %v", err)
	}
	if out != "plain\n" {
		t.Fatalf("CapturePane output = %q", out)
	}
	want := []string{"capture-pane", "-p", "-J", "-t", "%1", "-S", "-40"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("capture args = %#v, want %#v", gotArgs, want)
	}
}

func TestCapturePanePreservesColorsWhenEnabled(t *testing.T) {
	t.Parallel()

	var gotArgs []string
	c := &Client{bin: "tmux", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("\x1b[31mred\x1b[0m\n"), nil
	}}
	c.SetPreserveColors(true)

	out, err := c.CapturePane(context.Background(), "%1", 40)
	if err != nil {
		t.Fatalf("CapturePane returned error: %v", err)
	}
	if !strings.Contains(out, "\x1b[31m") {
		t.Fatalf("CapturePane should preserve ANSI output, got %q", out)
	}
	want := []string{"capture-pane", "-p", "-J", "-e", "-t", "%1", "-S", "-40"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("capture args = %#v, want %#v", gotArgs, want)
	}
}

func TestMonitorOnlyRefusesSendKeysAndKillSession(t *testing.T) {
	t.Parallel()

	c := &Client{bin: "tmux", run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("runner should not be called in monitor-only mode")
		return nil, nil
	}}
	c.SetMonitorOnly(true)

	if err := c.SendKeys(context.Background(), "%1", "Enter"); err == nil || !strings.Contains(err.Error(), "monitor-only") {
		t.Fatalf("SendKeys error = %v, want monitor-only refusal", err)
	}
	if err := c.KillSession(context.Background(), "$1"); err == nil || !strings.Contains(err.Error(), "monitor-only") {
		t.Fatalf("KillSession error = %v, want monitor-only refusal", err)
	}
}

func TestControlActionsUseInjectedRunner(t *testing.T) {
	t.Parallel()

	var commands [][]string
	c := &Client{bin: "tmux", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		commands = append(commands, append([]string(nil), args...))
		return nil, nil
	}}

	if err := c.SendKeys(context.Background(), "%1", "Enter"); err != nil {
		t.Fatalf("SendKeys returned error: %v", err)
	}
	if err := c.KillSession(context.Background(), "$1"); err != nil {
		t.Fatalf("KillSession returned error: %v", err)
	}

	want := [][]string{
		{"send-keys", "-t", "%1", "Enter"},
		{"kill-session", "-t", "$1"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}
