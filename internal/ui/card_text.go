package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	"github.com/mattn/go-runewidth"
)

func truncateSingleLine(value string, width int) string {
	value = cardSafeLine(value)
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	tail := "..."
	if width < lipgloss.Width(tail)+1 {
		tail = ""
	}
	return runewidth.Truncate(value, width, tail)
}

func cockpitTitleParts(session tmux.Session, window tmux.Window, pane tmux.Pane, host string) []string {
	if pane.Cockpit != nil {
		meta := pane.Cockpit
		if isOpenClawRuntimePane(pane) {
			runtime := strings.ToUpper(firstNonEmpty(meta.Agent, "runtime"))
			status := firstNonEmpty(meta.DisplayStatus, meta.State, "unknown")
			title := firstNonEmpty(meta.Goal, pane.TitleOrCmd(), session.Name)
			return dedupeTitleParts([]string{runtime, status, title})
		}
		if strings.EqualFold(strings.TrimSpace(meta.Kind), "service") {
			return dedupeTitleParts([]string{session.Name, "SERVICE"})
		}
		parts := []string{session.Name}
		if meta.DisplayOnly() {
			parts = append(parts, "ADOPTED")
		}
		if agent := strings.TrimSpace(meta.Agent); agent != "" {
			parts = append(parts, strings.ToUpper(agent))
		} else if kind := strings.TrimSpace(meta.Kind); kind != "" {
			parts = append(parts, strings.ToUpper(kind))
		}
		if owner := strings.TrimSpace(meta.Owner); owner != "" {
			parts = append(parts, owner)
		}
		if project := strings.TrimSpace(meta.Project); project != "" {
			parts = append(parts, project)
		}
		if len(parts) > 0 {
			return parts
		}
	}

	titleParts := dedupeTitleParts([]string{session.Name, window.Name})
	paneLabel := strings.TrimSpace(pane.TitleOrCmd())
	if host != "" && strings.EqualFold(strings.TrimSpace(paneLabel), strings.TrimSpace(host)) {
		paneLabel = ""
	}
	if paneLabel != "" {
		titleParts = dedupeTitleParts(append(titleParts, paneLabel))
	}
	return titleParts
}

func dedupeTitleParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}

func parseCockpitTimestamp(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func cockpitInfoLines(width int, m *Model, session tmux.Session, pane tmux.Pane, now time.Time) []string {
	if pane.Cockpit == nil {
		return nil
	}
	lines := []string{}
	goal := strings.TrimSpace(pane.Cockpit.Goal)
	if goal != "" {
		lines = append(lines, cockpitSubtleLine(width, "goal: "+goal))
	}
	if cleanup := cockpitCleanupLine(m, session, pane, now); cleanup != "" {
		lines = append(lines, cockpitSubtleLine(width, cleanup))
	}
	return lines
}

func cockpitCardInfoLines(width int, m *Model, session tmux.Session, pane tmux.Pane, sessionState string, now time.Time) []string {
	lines := []string{}
	if attention := cockpitAttentionLine(width, m, session, pane, sessionState); attention != "" {
		lines = append(lines, attention)
	}
	lines = append(lines, cockpitInfoLines(width, m, session, pane, now)...)
	if m != nil &&
		m.viewMode == viewModeDetail &&
		m.detailSession == session.ID &&
		isOpenClawRuntimePane(pane) {
		lines = append(lines, m.runtimeTimelineDetailLines(width, maxRuntimeTimelineDetailRows)...)
	}
	return lines
}

func cockpitAttentionLine(width int, m *Model, session tmux.Session, pane tmux.Pane, sessionState string) string {
	sessionState = strings.TrimSpace(sessionState)
	if sessionState == "" {
		return ""
	}
	activeState := paneAttentionState(m, session, pane)
	if activeState == sessionState {
		return ""
	}
	return cockpitSubtleLine(width, "attention: "+sessionState+" in another pane")
}

func cockpitSubtleLine(width int, line string) string {
	line = cardSafeLine(line)
	if width > 0 && lipgloss.Width(line) > width {
		line = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(line)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(line)
}

// attentionStateLabel maps an internal attention state to the chip shown in the
// card header. idle-finished carries the "finished · awaiting review" wording so
// a reclassified TUI pane reads as done-and-awaiting-review, not still running.
func attentionStateLabel(state string) string {
	if state == "idle-finished" {
		return "finished · awaiting review"
	}
	if state == "marked-for-teardown" {
		return "marked for teardown"
	}
	if state == "awaiting-operator" {
		return "waiting on operator"
	}
	return state
}

func compactFinishedBody(width int, body string, state string, maxLines int) string {
	switch state {
	case "done", "held", "stale", "failed", "route-fail", "safety-fail", "review", "pass", "signal", "directional", "null-safe", "idle-finished", "marked-for-teardown":
	default:
		return body
	}
	if maxLines <= 0 {
		maxLines = maxOverviewBodyLines
	}
	lines := trimTrailingBlankLines(strings.Split(body, "\n"))
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	hidden := len(lines) - maxLines
	kept := append([]string{}, lines[:maxLines]...)
	message := fmt.Sprintf("... %d more lines, open detail for transcript", hidden)
	if width > 0 && lipgloss.Width(message) > width {
		message = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(message)
	}
	kept = append(kept, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(message))
	return strings.Join(kept, "\n")
}

func compactOverviewBody(width int, body string, overview bool, maxLines int) string {
	if !overview {
		return body
	}
	if maxLines <= 0 {
		maxLines = maxOverviewBodyLines
	}
	lines := trimTrailingBlankLines(strings.Split(body, "\n"))
	if len(lines) == 0 {
		return ""
	}
	message := ""
	if len(lines) <= maxLines {
		for len(lines) < maxLines {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}
	keepLines := max(1, maxLines-1)
	kept := append([]string{}, lines[:keepLines]...)
	message = fmt.Sprintf("... %d more lines, open detail for full pane", len(lines)-len(kept))
	if width > 0 && lipgloss.Width(message) > width {
		message = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(message)
	}
	kept = append(kept, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(message))
	return strings.Join(kept, "\n")
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return append([]string{}, lines[:end]...)
}

func cockpitCleanupLine(m *Model, session tmux.Session, pane tmux.Pane, now time.Time) string {
	row, join := m.janitorSessionRow(session)
	hasRow := join == janitorJoinOK
	if pane.Cockpit == nil && (!hasRow || !row.ExistingSession) {
		return ""
	}
	meta := pane.Cockpit
	if meta == nil {
		meta = &tmux.CockpitMeta{}
	}
	parts := []string{}
	if hasRow && row.ExistingSession {
		label := "existing session: " + row.JanitorState + " · " + row.Reason
		if row.KillNotBefore != "" {
			label += " · " + janitorCountdownText(row, join, now)
		}
		parts = append(parts, label)
	} else if !pane.Dead && assignmentTerminalState(meta) != "" {
		parts = append(parts, "assignment complete · retained until CLI exits")
	} else if !pane.Dead {
		if verdict := m.cachedLifecycleVerdict(pane, session); verdict.state == "delivered-idle" {
			parts = append(parts, "assignment appears complete · CLI open")
		}
	}
	if pane.AlternateScreen {
		parts = append(parts, fmt.Sprintf("native screen %d×%d · open detail to inspect", pane.Width, pane.Height))
	}
	if policy := strings.TrimSpace(meta.CleanupPolicy); policy != "" {
		parts = append(parts, "policy: "+policy)
	}
	if ttl := strings.TrimSpace(meta.TTL); ttl != "" && ttl != "never" {
		parts = append(parts, "ttl: "+ttl)
	}
	hold := strings.TrimSpace(meta.HoldReason)
	marked := strings.TrimSpace(meta.TeardownMarkedAt)
	if hold != "" {
		label := "hold blocks cleanup: " + hold
		if until := parseCockpitTimestamp(meta.HoldUntil); !until.IsZero() {
			if !now.Before(until) {
				switch meta.Kind {
				case "service", "viewer", "runtime":
					label = "hold remains active for " + meta.Kind + ": " + hold
				default:
					label = "hold expired; cleanup rechecked by janitor: " + hold
				}
			} else {
				label += " · until " + until.Local().Format("Jan 2 15:04 MST")
			}
		}
		if marked != "" && (parseCockpitTimestamp(meta.HoldUntil).IsZero() || now.Before(parseCockpitTimestamp(meta.HoldUntil))) {
			// Held+marked is a conflict: the mark is inert while the hold
			// stands, so no countdown may render next to it.
			label += " · teardown mark inert (hold conflict)"
		}
		parts = append(parts, label)
	} else if marked != "" {
		label := "marked for teardown"
		if reason := strings.TrimSpace(meta.TeardownReason); reason != "" {
			label += ": " + reason
		}
		label += " · " + janitorCountdownText(row, join, now)
		parts = append(parts, label)
	} else if strings.EqualFold(strings.TrimSpace(meta.JanitorState), "cleanup_pending") {
		parts = append(parts, "cleanup pending")
	}
	if janitorRowCarriesCleanupAuthority(row) {
		switch join {
		case janitorJoinMismatch:
			// A fresh sidecar row exists under this session's name but describes
			// a different pane: say so instead of attaching its cleanup truth.
			parts = append(parts, "janitor row ignored: pane identity mismatch (stale row for a previous pane)")
		case janitorJoinMissingIdentity:
			parts = append(parts, "janitor row ignored: no pane identity (older status payload, treated as stale)")
		}
	}
	if hasRow && strings.EqualFold(strings.TrimSpace(row.JanitorState), "cleanup_blocked") {
		reason := strings.TrimSpace(row.LastRefusal)
		if reason == "" {
			reason = strings.TrimSpace(row.Reason)
		}
		if reason == "" {
			reason = "janitor refusal"
		}
		parts = append(parts, "cleanup blocked: "+reason)
	}
	if end := strings.TrimSpace(meta.EndReason); end != "" && end != "expected_exit" {
		parts = append(parts, "end: "+end)
	}
	if progress := displayEvidencePath(meta.ProgressPath); progress != "" {
		parts = append(parts, "progress: "+progress)
	}
	if evidence := cockpitEvidenceLine(meta); evidence != "" {
		parts = append(parts, evidence)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// janitorCountdownText renders the janitor-owned mark-to-kill countdown. The
// sidecar kill_not_before is the only countdown source: when the sidecar is
// missing, stale, identity-mismatched, or carries no kill_not_before, Cockpit
// says so instead of inventing a countdown from a local constant.
func janitorCountdownText(row janitorSessionStatus, join janitorJoinState, now time.Time) string {
	switch join {
	case janitorJoinOK:
		if killAt := parseCockpitTimestamp(row.KillNotBefore); !killAt.IsZero() {
			if remaining := killAt.Sub(now); remaining > 0 {
				return "cleanup in " + coarseDuration(remaining)
			}
			return "cleanup pending"
		}
		return "cleanup countdown unknown (no fresh janitor status)"
	case janitorJoinMismatch:
		return "cleanup countdown unavailable (janitor row is for a previous pane)"
	case janitorJoinMissingIdentity:
		return "cleanup countdown unavailable (janitor row has no pane identity)"
	default:
		return "cleanup countdown unknown (no fresh janitor status)"
	}
}

func cockpitGroupBadge(meta *tmux.CockpitMeta) string {
	if meta == nil {
		return ""
	}
	count := cockpitIntField(meta.GroupedRecordCount)
	if count <= 1 {
		return ""
	}
	return fmt.Sprintf("x%d", count)
}

func cockpitEvidenceLine(meta *tmux.CockpitMeta) string {
	if meta == nil {
		return ""
	}
	evidence := strings.TrimSpace(meta.EvidencePath)
	if evidence == "" {
		return ""
	}
	grouped := cockpitIntField(meta.GroupedRecordCount)
	if grouped > 1 {
		evidenceCount := countEvidenceIDs(evidence)
		if evidenceCount == 0 {
			evidenceCount = grouped
		}
		return fmt.Sprintf("evidence: %d ids, open detail for full list", evidenceCount)
	}
	if value := displayEvidencePath(evidence); value != "" {
		return "evidence: " + value
	}
	return ""
}

func countEvidenceIDs(value string) int {
	count := 0
	for _, part := range strings.Split(value, ",") {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

func displayEvidencePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return ""
	}
	base := filepath.Base(cleaned)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}
