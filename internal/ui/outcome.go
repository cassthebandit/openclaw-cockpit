package ui

import (
	"encoding/json"
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
	if status, _ := payload["status"].(string); strings.EqualFold(status, "FAIL") {
		return "failed"
	}
	for _, key := range []string{"result_classification", "classification"} {
		if value, _ := payload[key].(string); value != "" {
			return classificationState(value)
		}
	}
	if status, _ := payload["status"].(string); strings.EqualFold(status, "PASS") {
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
	case "done", "pass", "signal", "directional", "null-safe", "idle-finished":
		return true
	default:
		return false
	}
}

func stateNeedsAttention(state string) bool {
	switch state {
	case "failed", "route-fail", "safety-fail", "review", "waiting", "blocked":
		return true
	default:
		return false
	}
}

// stateIsLiveAgentState reports the sub-states that keep a managed agent in the
// Interactive Agents band. It is deliberately broader than stateIsActiveRun so
// waiting/blocked/review agents are not inherited from stateNeedsAttention and
// pushed into System Problems.
func stateIsLiveAgentState(state string) bool {
	switch state {
	case "starting", "running", "waiting", "blocked", "review":
		return true
	default:
		return false
	}
}

// stateIsTerminalProblem reports the terminal/stale states that move a managed
// agent to System Problems (never a live blocked/waiting agent).
func stateIsTerminalProblem(state string) bool {
	switch state {
	case "failed", "route-fail", "safety-fail", "stale":
		return true
	default:
		return false
	}
}

func stateIsActiveRun(state string) bool {
	return state == "running" || state == "starting"
}
