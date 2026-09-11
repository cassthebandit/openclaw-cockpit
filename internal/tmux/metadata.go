package tmux

import "strings"

// Ordered wire fields generate the current format and its parser together.
// Legacy 33/37/41/42-column rows remain supported; before the 42-column
// revision PaneLog was absent, so the tail has a distinct wire position.
type metadataField struct {
	key   string
	field func(*CockpitMeta) *string
}

var metadataFields = []metadataField{
	{"@oc_contract_version", func(m *CockpitMeta) *string { return &m.ContractVersion }},
	{"@oc_managed_by", func(m *CockpitMeta) *string { return &m.ManagedBy }},
	{"@oc_kind", func(m *CockpitMeta) *string { return &m.Kind }},
	{"@oc_agent", func(m *CockpitMeta) *string { return &m.Agent }},
	{"@oc_owner", func(m *CockpitMeta) *string { return &m.Owner }},
	{"@oc_project", func(m *CockpitMeta) *string { return &m.Project }},
	{"@oc_goal", func(m *CockpitMeta) *string { return &m.Goal }},
	{"@oc_state", func(m *CockpitMeta) *string { return &m.State }},
	{"@oc_run_root", func(m *CockpitMeta) *string { return &m.RunRoot }},
	{"@oc_thread_id", func(m *CockpitMeta) *string { return &m.ThreadID }},
	{"@oc_session_id", func(m *CockpitMeta) *string { return &m.SessionID }},
	{"@oc_started_at", func(m *CockpitMeta) *string { return &m.StartedAt }},
	{"@oc_updated_at", func(m *CockpitMeta) *string { return &m.UpdatedAt }},
	{"@oc_completed_at", func(m *CockpitMeta) *string { return &m.CompletedAt }},
	{"@oc_exit_code", func(m *CockpitMeta) *string { return &m.ExitCode }},
	{"@oc_ttl", func(m *CockpitMeta) *string { return &m.TTL }},
	{"@oc_cleanup_policy", func(m *CockpitMeta) *string { return &m.CleanupPolicy }},
	{"@oc_evidence_path", func(m *CockpitMeta) *string { return &m.EvidencePath }},
	{"@oc_hold_reason", func(m *CockpitMeta) *string { return &m.HoldReason }},
	{"@oc_why_headless", func(m *CockpitMeta) *string { return &m.WhyHeadless }},
	{"@oc_pane_log", func(m *CockpitMeta) *string { return &m.PaneLog }},
	{"@oc_progress_path", func(m *CockpitMeta) *string { return &m.ProgressPath }},
	{"@oc_end_reason", func(m *CockpitMeta) *string { return &m.EndReason }},
	{"@oc_route_failure_reason", func(m *CockpitMeta) *string { return &m.RouteFailure }},
	{"@oc_teardown_marked_at", func(m *CockpitMeta) *string { return &m.TeardownMarkedAt }},
	{"@oc_teardown_reason", func(m *CockpitMeta) *string { return &m.TeardownReason }},
	{"@oc_janitor_state", func(m *CockpitMeta) *string { return &m.JanitorState }},
	{"@oc_last_meaningful_activity_at", func(m *CockpitMeta) *string { return &m.LastMeaningfulAt }},
	{"@oc_hold_until", func(m *CockpitMeta) *string { return &m.HoldUntil }},
}

func paneListFormat() string {
	keys := []string{"session_id", "window_id", "pane_id", "pane_active", "pane_current_command", "pane_title", "pane_last_activity", "pane_created", "pane_width", "pane_height", "pane_tty", "pane_current_path", "pane_dead", "pane_dead_status"}
	for _, field := range metadataFields {
		keys = append(keys, field.key)
	}
	keys = append(keys, "pane_pid", "alternate_on")
	return escapedTmuxFormat(keys...)
}
func parsePaneMetadata(fields []string) *CockpitMeta {
	if len(fields) < 33 {
		return nil
	}
	var meta CockpitMeta
	index := 14
	for _, field := range metadataFields {
		if len(fields) < 42 && field.key == "@oc_pane_log" {
			continue
		}
		if index >= len(fields) || index >= 43 {
			break
		}
		*field.field(&meta) = strings.TrimSpace(fields[index])
		index++
	}
	if !meta.HasData() {
		return nil
	}
	return &meta
}
