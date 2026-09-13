// Package tmux exposes helpers for translating tmux command output into rich
// Go structs the UI can consume.
package tmux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Field framing. A printable separator is required (when a Go subprocess
// invokes tmux from inside a tmux-launched process, tmux can rewrite control
// characters in format strings to underscores), but a bare printable sentinel
// collides with free-text content: window names and pane titles preserve any
// printable string, and several accepted @oc_* options can carry arbitrary
// text. Framing is therefore escape-based:
//
//   - every field in every list format is wrapped in a tmux substitution that
//     rewrites "~" to "~e" inside the value (escapedTmuxFormat);
//   - an escaped value can never contain two adjacent tildes, so the "~~"
//     separator cannot occur inside any field;
//   - decodeTmuxField reverses the escape after splitting.
//
// The tmux `s` modifier cannot substitute ":" (the format parser cuts the
// modifier at any colon, even inside alternate delimiters), which rules out
// escaping a colon-based sentinel; a tilde-only escape needs exactly one
// substitution per field and was verified against tmux 3.6b.
const (
	tmuxFieldSep    = "~~"
	tmuxFieldEscape = "~e"
	// tmuxRowCapBytes is the documented hard cap for one list-command output
	// row. Aggregate valid metadata around 70 KB must parse (several accepted
	// @oc_* options can legitimately reach that), while a pathological row
	// above the cap fails visibly instead of silently truncating the wall.
	tmuxRowCapBytes = 1 << 20 // 1 MiB
)

// escapedTmuxFormat joins tmux format variables into one row format with
// every field tilde-escaped so the field separator cannot collide.
func escapedTmuxFormat(names ...string) string {
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = "#{s|~|" + tmuxFieldEscape + "|:" + name + "}"
	}
	return strings.Join(parts, tmuxFieldSep)
}

// splitTmuxRow splits an escaped output row and decodes each field.
func splitTmuxRow(line string) []string {
	fields := strings.Split(line, tmuxFieldSep)
	for i, field := range fields {
		fields[i] = strings.ReplaceAll(field, tmuxFieldEscape, "~")
	}
	return fields
}

// newRowScanner wraps list-command output in a Scanner with an explicit
// per-row token cap, so an oversized row surfaces bufio.ErrTooLong instead of
// failing at the default 64 KB token limit or allocating without bound.
func newRowScanner(out []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 64*1024), tmuxRowCapBytes)
	return scanner
}

// rowScanErr converts a Scanner error into a visible, actionable list error.
func rowScanErr(command string, err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("%s: output row exceeds the %d byte cap: %w", command, tmuxRowCapBytes, err)
	}
	return fmt.Errorf("%s: %w", command, err)
}

func acceptedPaneFieldCount(count int) bool {
	return count == 14 || count == 33 || count == 37 || count == 41 || count == 42 || count == 45
}

// listSessions shells out to tmux to enumerate sessions and translate them
// into typed Session values.
func (c *Client) listSessions(ctx context.Context) ([]Session, error) {
	out, err := c.runTmux(ctx, "list-sessions", "-F", escapedTmuxFormat(
		"session_id",
		"session_name",
		"session_attached",
		"session_created",
		"session_activity",
	))
	if err != nil {
		if isNoServerError(err) {
			return []Session{}, nil
		}
		return nil, fmt.Errorf("list-sessions: %w", err)
	}
	scanner := newRowScanner(out)
	sessions := []Session{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitTmuxRow(line)
		if len(fields) != 5 {
			return nil, fmt.Errorf("list-sessions: malformed line %q", line)
		}
		attachedCount, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid session_attached %q: %w", fields[2], err)
		}
		attached := attachedCount > 0
		createdUnix, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid session_created %q: %w", fields[3], err)
		}
		lastActivity, err := parseUnix(fields[4])
		if err != nil {
			return nil, fmt.Errorf("invalid session_activity %q: %w", fields[4], err)
		}
		session := Session{
			ID:           fields[0],
			Name:         fields[1],
			Attached:     attached,
			CreatedAt:    time.Unix(createdUnix, 0),
			LastActivity: lastActivity,
		}
		sessions = append(sessions, session)
	}
	if err := scanner.Err(); err != nil {
		return nil, rowScanErr("list-sessions", err)
	}
	return sessions, nil
}

// listWindows retrieves every window in every session so we can later nest
// panes under them.
func (c *Client) listWindows(ctx context.Context) ([]Window, error) {
	out, err := c.runTmux(ctx, "list-windows", "-a", "-F", escapedTmuxFormat(
		"session_id",
		"window_id",
		"window_index",
		"window_name",
		"window_active",
		"window_last_flag",
	))
	if err != nil {
		if isNoServerError(err) {
			return []Window{}, nil
		}
		return nil, fmt.Errorf("list-windows: %w", err)
	}
	scanner := newRowScanner(out)
	windows := []Window{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitTmuxRow(line)
		if len(fields) != 6 {
			return nil, fmt.Errorf("list-windows: malformed line %q", line)
		}
		index, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("invalid window_index %q: %w", fields[2], err)
		}
		active := fields[4] == "1"
		window := Window{
			Session: fields[0],
			ID:      fields[1],
			Index:   index,
			Name:    fields[3],
			Active:  active,
		}
		windows = append(windows, window)
	}
	if err := scanner.Err(); err != nil {
		return nil, rowScanErr("list-windows", err)
	}
	return windows, nil
}

// listPanes captures metadata for every pane so we can join them to windows
// and sessions.
func (c *Client) listPanes(ctx context.Context) ([]Pane, int, error) {
	format := paneListFormat()

	out, err := c.runTmux(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		if isNoServerError(err) {
			return []Pane{}, 0, nil
		}
		return nil, 0, fmt.Errorf("list-panes: %w", err)
	}
	scanner := newRowScanner(out)
	panes := []Pane{}
	skipped := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitTmuxRow(line)
		if !acceptedPaneFieldCount(len(fields)) {
			skipped++
			continue
		}
		active := fields[3] == "1"
		lastActivity, err := parseUnix(fields[6])
		if err != nil {
			skipped++
			continue
		}
		created, err := parseUnix(fields[7])
		if err != nil {
			skipped++
			continue
		}
		width, err := strconv.Atoi(fields[8])
		if err != nil {
			skipped++
			continue
		}
		height, err := strconv.Atoi(fields[9])
		if err != nil {
			skipped++
			continue
		}
		pane := Pane{
			Session:      fields[0],
			Window:       fields[1],
			ID:           fields[2],
			Active:       active,
			CurrentCmd:   fields[4],
			Title:        fields[5],
			LastActivity: lastActivity,
			CreatedAt:    created,
			Width:        width,
			Height:       height,
			TTY:          fields[10],
			CurrentPath:  fields[11],
			Dead:         fields[12] == "1",
		}
		if status := strings.TrimSpace(fields[13]); status != "" {
			if v, err := strconv.Atoi(status); err == nil {
				pane.DeadStatus = v
			}
		}
		pane.Cockpit = parsePaneMetadata(fields)
		if len(fields) >= 45 {
			pane.PID = strings.TrimSpace(fields[43])
			pane.AlternateScreen = fields[44] == "1"
		}

		panes = append(panes, pane)
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, rowScanErr("list-panes", err)
	}
	if skipped > 0 {
		log.Printf("tmux list-panes: skipped %d malformed pane row(s)", skipped)
	}
	return panes, skipped, nil
}

// parseUnix converts tmux's unix timestamp fields into a time value.
func parseUnix(v string) (time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return time.Time{}, nil
	}
	iv, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(iv, 0), nil
}
