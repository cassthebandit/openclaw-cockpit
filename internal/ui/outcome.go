package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

type cockpitOutcome struct {
	state string
}

func semanticPaneOutcome(pane tmux.Pane) cockpitOutcome {
	if pane.Cockpit == nil {
		return cockpitOutcome{}
	}
	if pane.Dead && pane.DeadStatus != 0 {
		return cockpitOutcome{}
	}
	if state := artifactOutcomeState(pane); state != "" {
		return cockpitOutcome{state: state}
	}
	meta := pane.Cockpit
	if strings.TrimSpace(meta.RouteFailure) != "" {
		return cockpitOutcome{state: "route-fail"}
	}
	if strings.Contains(strings.ToLower(meta.EndReason), "safety") {
		return cockpitOutcome{state: "safety-fail"}
	}
	return cockpitOutcome{}
}

func (m *Model) semanticPaneOutcome(pane tmux.Pane) cockpitOutcome {
	if pane.Cockpit == nil {
		return cockpitOutcome{}
	}
	if pane.Dead && pane.DeadStatus != 0 {
		return cockpitOutcome{}
	}
	if state := m.cachedArtifactOutcome(pane); state != "" {
		return cockpitOutcome{state: state}
	}
	meta := pane.Cockpit
	if strings.TrimSpace(meta.RouteFailure) != "" {
		return cockpitOutcome{state: "route-fail"}
	}
	if strings.Contains(strings.ToLower(meta.EndReason), "safety") {
		return cockpitOutcome{state: "safety-fail"}
	}
	return cockpitOutcome{}
}

// artifactOutcomeProbe caches an evidence outcome together with the stat
// identity (mtime+size) of the files it was derived from, so the per-snapshot
// refresh only re-reads and re-parses evidence when something on disk actually
// changed. Refresh runs on the Update goroutine; unbounded ReadFile+JSON per
// pane per second was a measurable input-hitch source.
type artifactOutcomeProbe struct {
	statKey string
	state   string
}

func (m *Model) refreshArtifactOutcomes() {
	if m == nil {
		return
	}
	next := make(map[string]string)
	probes := make(map[string]artifactOutcomeProbe)
	for _, session := range m.sessions {
		for _, window := range session.Windows {
			for _, pane := range window.Panes {
				key := artifactOutcomeCacheKey(pane)
				if key == "" {
					continue
				}
				statKey := artifactStatKey(pane)
				if prev, ok := m.artifactProbes[key]; ok && statKey != "" && prev.statKey == statKey {
					if prev.state != "" {
						next[key] = prev.state
					}
					probes[key] = prev
					continue
				}
				state := artifactOutcomeState(pane)
				if state != "" {
					next[key] = state
				}
				probes[key] = artifactOutcomeProbe{statKey: statKey, state: state}
			}
		}
	}
	m.artifactOutcomes = next
	m.artifactProbes = probes
}

// artifactStatKey fingerprints the evidence path and its candidate outcome
// files by mtime+size. Returns "" when the path cannot be resolved, which
// disables probe reuse for that pane (full recompute each snapshot, matching
// the old behaviour).
func artifactStatKey(pane tmux.Pane) string {
	meta := pane.Cockpit
	if meta == nil {
		return ""
	}
	runRoot := strings.TrimSpace(meta.RunRoot)
	evidence := strings.TrimSpace(meta.EvidencePath)
	if runRoot == "" || evidence == "" {
		return ""
	}
	root, err := filepath.Abs(filepath.Clean(runRoot))
	if err != nil {
		return ""
	}
	evidencePath := evidence
	if !filepath.IsAbs(evidencePath) {
		evidencePath = filepath.Join(root, evidencePath)
	}
	evidencePath, err = filepath.Abs(filepath.Clean(evidencePath))
	if err != nil {
		return ""
	}
	var b strings.Builder
	appendStat := func(path string) os.FileInfo {
		info, statErr := os.Stat(path)
		if statErr != nil {
			b.WriteString(path)
			b.WriteString("=missing;")
			return nil
		}
		fmt.Fprintf(&b, "%s=%d:%d;", path, info.ModTime().UnixNano(), info.Size())
		return info
	}
	if info := appendStat(evidencePath); info != nil && info.IsDir() {
		for _, name := range []string{"verification.json", "analysis.json", "summary.json", "RESULT.md"} {
			appendStat(filepath.Join(evidencePath, name))
		}
	}
	return b.String()
}

func (m *Model) cachedArtifactOutcome(pane tmux.Pane) string {
	if m == nil || len(m.artifactOutcomes) == 0 {
		return ""
	}
	return m.artifactOutcomes[artifactOutcomeCacheKey(pane)]
}

func artifactOutcomeCacheKey(pane tmux.Pane) string {
	if pane.Cockpit == nil {
		return ""
	}
	runRoot := strings.TrimSpace(pane.Cockpit.RunRoot)
	evidence := strings.TrimSpace(pane.Cockpit.EvidencePath)
	if runRoot == "" || evidence == "" {
		return ""
	}
	return strings.Join([]string{pane.ID, runRoot, evidence}, "\x00")
}

func artifactOutcomeState(pane tmux.Pane) string {
	meta := pane.Cockpit
	if meta == nil {
		return ""
	}
	runRoot := strings.TrimSpace(meta.RunRoot)
	evidence := strings.TrimSpace(meta.EvidencePath)
	if runRoot == "" || evidence == "" {
		return ""
	}
	root, err := filepath.Abs(filepath.Clean(runRoot))
	if err != nil {
		return ""
	}
	evidencePath := evidence
	if !filepath.IsAbs(evidencePath) {
		evidencePath = filepath.Join(root, evidencePath)
	}
	evidencePath, err = filepath.Abs(filepath.Clean(evidencePath))
	if err != nil || !pathIsInside(root, evidencePath) {
		return "review"
	}
	info, err := os.Stat(evidencePath)
	if err != nil {
		return "review"
	}
	if info.IsDir() {
		for _, name := range []string{"verification.json", "analysis.json", "summary.json", "RESULT.md"} {
			if state := artifactFileOutcome(filepath.Join(evidencePath, name), false); state != "" {
				return state
			}
		}
		return ""
	}
	return artifactFileOutcome(evidencePath, true)
}

func artifactFileOutcome(path string, required bool) string {
	info, err := os.Stat(path)
	if err != nil {
		if required {
			return "review"
		}
		return ""
	}
	if info.IsDir() || info.Size() > 1_000_000 {
		return "review"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return artifactJSONOutcome(path)
	default:
		if state := artifactTextOutcome(path); state != "" {
			return state
		}
	}
	return ""
}

func pathIsInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func artifactJSONOutcome(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "review"
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	status, hasStatus := payload["status"].(string)
	if hasStatus && strings.EqualFold(status, "FAIL") {
		return "failed"
	}
	for _, key := range []string{"result_classification", "classification"} {
		if value, ok := payload[key].(string); ok && value != "" {
			return classificationState(value)
		}
	}
	if hasStatus && strings.EqualFold(status, "PASS") {
		return "pass"
	}
	return ""
}

func artifactTextOutcome(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "review"
	}
	text := strings.ToLower(string(data))
	for _, prefix := range []string{"classification:", "result_classification"} {
		idx := strings.Index(text, prefix)
		if idx < 0 {
			continue
		}
		line := text[idx:]
		if newline := strings.IndexByte(line, '\n'); newline >= 0 {
			line = line[:newline]
		}
		return classificationState(extractClassificationValue(line))
	}
	return ""
}

func classificationState(value string) string {
	value = normalizeClassification(value)
	switch value {
	case "route_fail", "route_failed":
		return "route-fail"
	case "safety_fail", "safety_failed":
		return "safety-fail"
	case "fail", "failed":
		return "failed"
	case "signal_ok":
		return "signal"
	case "directional", "directional_signal":
		return "directional"
	case "null_safe", "live_microarm_null_safe", "smoke_null", "smoke_null_safe":
		return "null-safe"
	case "inconclusive", "degraded":
		return "review"
	case "pass", "passed":
		return "pass"
	default:
		return ""
	}
}

func extractClassificationValue(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.IndexAny(line, ":="); idx >= 0 {
		line = line[idx+1:]
	}
	return line
}

func normalizeClassification(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, "`'\" ")
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	if len(fields) == 1 {
		return fields[0]
	}
	return value
}

func stateIsCompletedInfo(state string) bool {
	switch state {
	case "done", "pass", "signal", "directional", "null-safe", "idle-finished", "delivered-idle", "terminal-done":
		return true
	default:
		return false
	}
}

func stateNeedsAttention(state string) bool {
	switch state {
	case "failed", "route-fail", "safety-fail", "terminal-problem", "awaiting-operator", "review", "waiting", "blocked":
		return true
	default:
		return false
	}
}

// stateIsLiveAgentState reports the sub-states that keep a managed agent in the
// Active Agents band. It is deliberately broader than just running/starting so
// waiting/blocked/review agents are not inherited from stateNeedsAttention and
// pushed into Sub-System Failures.
func stateIsLiveAgentState(state string) bool {
	switch state {
	case "starting", "running", "waiting", "blocked", "review", "live-working":
		return true
	default:
		return false
	}
}

// stateIsTerminalProblem reports the failed terminal states that move a managed
// agent to Failed Agents. Stale readable panes are inactive cleanup debt unless
// stronger failure evidence exists.
func stateIsTerminalProblem(state string) bool {
	switch state {
	case "failed", "route-fail", "safety-fail", "terminal-problem":
		return true
	default:
		return false
	}
}
