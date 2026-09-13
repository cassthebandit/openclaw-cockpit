// Package tmux handles communication with the tmux binary for snapshotting and
// interacting with running panes.
package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"time"
)

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type pathLookup func(string) (string, error)

// Client wraps a tmux binary path and exposes high-level snapshot helpers.
type Client struct {
	bin               string
	run               commandRunner
	preserveColors    bool
	excludeSessions   map[string]bool
	monitorOnly       bool
	nativeSizeAllowed bool
}

// NewClient constructs a Client using the provided tmux binary. When tmuxPath
// is empty, LookPath("tmux") is used so the system PATH controls discovery.
func NewClient(tmuxPath string) (*Client, error) {
	return newClient(tmuxPath, exec.LookPath)
}

func newClient(tmuxPath string, lookup pathLookup) (*Client, error) {
	if tmuxPath == "" {
		var err error
		tmuxPath, err = lookup("tmux")
		if err != nil {
			return nil, fmt.Errorf("tmux not found in PATH (install tmux >=3.1): %w", err)
		}
	}
	return &Client{bin: tmuxPath, run: runCommand}, nil
}

// SetPreserveColors controls whether CapturePane preserves tmux escape
// sequences/colours in preview text.
func (c *Client) SetPreserveColors(enabled bool) {
	c.preserveColors = enabled
}

// SetExcludedSessions hides sessions with matching names from snapshots.
func (c *Client) SetExcludedSessions(names []string) {
	if len(names) == 0 {
		c.excludeSessions = nil
		return
	}
	c.excludeSessions = make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			c.excludeSessions[name] = true
		}
	}
}

// SetMonitorOnly makes control operations fail closed. Snapshot and capture
// operations remain available.
func (c *Client) SetMonitorOnly(enabled bool) {
	c.monitorOnly = enabled
}

// SetNativeSizeAllowed explicitly permits geometry changes without permitting input.
func (c *Client) SetNativeSizeAllowed(enabled bool) { c.nativeSizeAllowed = enabled }

func runCommand(ctx context.Context, bin string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, bin, args...).Output()
}

func (c *Client) runTmux(ctx context.Context, args ...string) ([]byte, error) {
	runner := c.run
	if runner == nil {
		runner = runCommand
	}
	return runner(ctx, c.bin, args...)
}

// Snapshot queries tmux for sessions, windows, and panes and returns a unified
// structure ready for presentation.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	sessions, err := c.listSessions(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if len(c.excludeSessions) > 0 {
		filtered := sessions[:0]
		for _, session := range sessions {
			if !c.excludeSessions[session.Name] {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}

	windows, err := c.listWindows(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	panes, skippedPanes, err := c.listPanes(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	type windowContext struct{ session, window string }
	windowMap := make(map[windowContext]*Window, len(windows))
	for i := range windows {
		windowMap[windowContext{windows[i].Session, windows[i].ID}] = &windows[i]
	}

	sessionMap := make(map[string]*Session, len(sessions))
	for i := range sessions {
		sessionMap[sessions[i].ID] = &sessions[i]
	}

	for _, pane := range panes {
		if win, ok := windowMap[windowContext{pane.Session, pane.Window}]; ok {
			win.Panes = append(win.Panes, pane)
			if pane.LastActivity.After(win.LastPane) {
				win.LastPane = pane.LastActivity
			}
		}
	}

	for _, window := range windows {
		if session, ok := sessionMap[window.Session]; ok {
			session.Windows = append(session.Windows, window)
		}
	}

	return Snapshot{
		Sessions:          slices.Clone(sessions),
		Timestamp:         time.Now(),
		PaneParseWarnings: skippedPanes,
	}, nil
}

// CapturePane retrieves lines of output from a tmux pane for preview rendering.
func (c *Client) CapturePane(ctx context.Context, paneID string, lines int) (string, error) {
	if paneID == "" {
		return "", fmt.Errorf("pane id cannot be empty")
	}
	if lines <= 0 {
		lines = 200
	}
	start := fmt.Sprintf("-%d", lines)
	args := []string{"capture-pane", "-p", "-J"}
	if c.preserveColors {
		args = append(args, "-e")
	}
	args = append(args, "-t", paneID, "-S", start)
	out, err := c.runTmux(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("capture-pane %s: %w", paneID, err)
	}
	return string(out), nil
}

// SendKeys forwards named tmux key tokens (Enter, Up, C-c, ...) to a pane.
// Printable text typed by the operator must use SendLiteralKeys instead so it
// is never interpreted as key names. The "--" guard keeps a token that starts
// with "-" from being read as a send-keys flag.
func (c *Client) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	if paneID == "" {
		return fmt.Errorf("pane id cannot be empty")
	}
	if len(keys) == 0 {
		return nil
	}
	if c.monitorOnly {
		return fmt.Errorf("send-keys refused: monitor-only mode is enabled")
	}
	args := append([]string{"send-keys", "-t", paneID, "--"}, keys...)
	if _, err := c.runTmux(ctx, args...); err != nil {
		return fmt.Errorf("send-keys %s: %w", paneID, err)
	}
	return nil
}

// SendLiteralKeys forwards printable operator input verbatim using tmux
// literal semantics (send-keys -l), so text such as ";", "C-c", or "Enter"
// typed as characters reaches the pane as those characters, never as key
// tokens. Monitor-only mode fails closed exactly like SendKeys.
func (c *Client) SendLiteralKeys(ctx context.Context, paneID string, text string) error {
	if paneID == "" {
		return fmt.Errorf("pane id cannot be empty")
	}
	if text == "" {
		return nil
	}
	if c.monitorOnly {
		return fmt.Errorf("send-keys refused: monitor-only mode is enabled")
	}
	if _, err := c.runTmux(ctx, "send-keys", "-l", "-t", paneID, "--", text); err != nil {
		return fmt.Errorf("send-keys -l %s: %w", paneID, err)
	}
	return nil
}

// KillSession terminates a tmux session by id.
func (c *Client) KillSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id cannot be empty")
	}
	if c.monitorOnly {
		return fmt.Errorf("kill-session refused: monitor-only mode is enabled")
	}
	if _, err := c.runTmux(ctx, "kill-session", "-t", sessionID); err != nil {
		return fmt.Errorf("kill-session %s: %w", sessionID, err)
	}
	return nil
}
