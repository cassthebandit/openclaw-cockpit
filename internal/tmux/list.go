// Package tmux exposes helpers for translating tmux command output into rich
// Go structs the UI can consume.
package tmux

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Use a printable sentinel instead of an ASCII control separator. When a Go
// subprocess invokes tmux from inside a tmux-launched process, tmux can rewrite
// control characters in format strings to underscores, which breaks parsing for
// real session names like "AI-Alerts".
const tmuxFieldSep = "::OC_FIELD::"

func acceptedPaneFieldCount(count int) bool {
	return count == 14 || count == 33 || count == 37 || count == 41 || count == 42
}

// listSessions shells out to tmux to enumerate sessions and translate them
// into typed Session values.
func (c *Client) listSessions(ctx context.Context) ([]Session, error) {
	out, err := c.runTmux(ctx, "list-sessions", "-F", strings.Join([]string{
		"#{session_id}",
		"#{session_name}",
		"#{session_attached}",
		"#{session_created}",
		"#{session_activity}",
	}, tmuxFieldSep))
	if err != nil {
		if isNoServerError(err) {
			return []Session{}, nil
		}
		return nil, fmt.Errorf("list-sessions: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	sessions := []Session{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, tmuxFieldSep)
		if len(fields) != 5 {
			return nil, fmt.Errorf("list-sessions: malformed line %q", line)
		}
		attached := fields[2] == "1"
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
		return nil, err
	}
	return sessions, nil
}

// listWindows retrieves every window in every session so we can later nest
// panes under them.
func (c *Client) listWindows(ctx context.Context) ([]Window, error) {
	out, err := c.runTmux(ctx, "list-windows", "-a", "-F", strings.Join([]string{
		"#{session_id}",
		"#{window_id}",
		"#{window_index}",
		"#{window_name}",
		"#{window_active}",
		"#{window_last_flag}",
	}, tmuxFieldSep))
	if err != nil {
		if isNoServerError(err) {
			return []Window{}, nil
		}
		return nil, fmt.Errorf("list-windows: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	windows := []Window{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, tmuxFieldSep)
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
		return nil, err
	}
	return windows, nil
}

// listPanes captures metadata for every pane so we can join them to windows
// and sessions.
func (c *Client) listPanes(ctx context.Context) ([]Pane, int, error) {
	format := strings.Join([]string{
		"#{session_id}",
		"#{window_id}",
		"#{pane_id}",
		"#{pane_active}",
		"#{pane_current_command}",
		"#{pane_title}",
		"#{pane_last_activity}",
		"#{pane_created}",
		"#{pane_width}",
		"#{pane_height}",
		"#{pane_tty}",
		"#{pane_current_path}",
		"#{pane_dead}",
		"#{pane_dead_status}",
		"#{@oc_contract_version}",
		"#{@oc_managed_by}",
		"#{@oc_kind}",
		"#{@oc_agent}",
		"#{@oc_owner}",
		"#{@oc_project}",
		"#{@oc_goal}",
		"#{@oc_state}",
		"#{@oc_run_root}",
		"#{@oc_thread_id}",
		"#{@oc_session_id}",
		"#{@oc_started_at}",
		"#{@oc_updated_at}",
		"#{@oc_completed_at}",
		"#{@oc_exit_code}",
		"#{@oc_ttl}",
		"#{@oc_cleanup_policy}",
		"#{@oc_evidence_path}",
		"#{@oc_hold_reason}",
		"#{@oc_why_headless}",
		"#{@oc_pane_log}",
		"#{@oc_progress_path}",
		"#{@oc_end_reason}",
		"#{@oc_route_failure_reason}",
		"#{@oc_teardown_marked_at}",
		"#{@oc_teardown_reason}",
		"#{@oc_janitor_state}",
		"#{@oc_last_meaningful_activity_at}",
	}, tmuxFieldSep)

	out, err := c.runTmux(ctx, "list-panes", "-a", "-F", format)
	if err != nil {
		if isNoServerError(err) {
			return []Pane{}, 0, nil
		}
		return nil, 0, fmt.Errorf("list-panes: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	panes := []Pane{}
	skipped := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, tmuxFieldSep)
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
		if len(fields) >= 33 {
			meta := CockpitMeta{
				ContractVersion: strings.TrimSpace(fields[14]),
				ManagedBy:       strings.TrimSpace(fields[15]),
				Kind:            strings.TrimSpace(fields[16]),
				Agent:           strings.TrimSpace(fields[17]),
				Owner:           strings.TrimSpace(fields[18]),
				Project:         strings.TrimSpace(fields[19]),
				Goal:            strings.TrimSpace(fields[20]),
				State:           strings.TrimSpace(fields[21]),
				RunRoot:         strings.TrimSpace(fields[22]),
				ThreadID:        strings.TrimSpace(fields[23]),
				SessionID:       strings.TrimSpace(fields[24]),
				StartedAt:       strings.TrimSpace(fields[25]),
				UpdatedAt:       strings.TrimSpace(fields[26]),
				CompletedAt:     strings.TrimSpace(fields[27]),
				ExitCode:        strings.TrimSpace(fields[28]),
				TTL:             strings.TrimSpace(fields[29]),
				CleanupPolicy:   strings.TrimSpace(fields[30]),
				EvidencePath:    strings.TrimSpace(fields[31]),
				HoldReason:      strings.TrimSpace(fields[32]),
			}
			if len(fields) >= 37 {
				meta.WhyHeadless = strings.TrimSpace(fields[33])
				meta.ProgressPath = strings.TrimSpace(fields[34])
				meta.EndReason = strings.TrimSpace(fields[35])
				meta.RouteFailure = strings.TrimSpace(fields[36])
			}
			if len(fields) >= 41 {
				meta.TeardownMarkedAt = strings.TrimSpace(fields[37])
				meta.TeardownReason = strings.TrimSpace(fields[38])
				meta.JanitorState = strings.TrimSpace(fields[39])
				meta.LastMeaningfulAt = strings.TrimSpace(fields[40])
			}
			if len(fields) >= 42 {
				meta.PaneLog = strings.TrimSpace(fields[34])
				meta.ProgressPath = strings.TrimSpace(fields[35])
				meta.EndReason = strings.TrimSpace(fields[36])
				meta.RouteFailure = strings.TrimSpace(fields[37])
				meta.TeardownMarkedAt = strings.TrimSpace(fields[38])
				meta.TeardownReason = strings.TrimSpace(fields[39])
				meta.JanitorState = strings.TrimSpace(fields[40])
				meta.LastMeaningfulAt = strings.TrimSpace(fields[41])
			}
			if meta.HasData() {
				pane.Cockpit = &meta
			}
		}
		panes = append(panes, pane)
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, err
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
