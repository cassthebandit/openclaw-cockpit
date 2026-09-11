package ui

import (
	"slices"
	"strings"
)

// GroupingConfig customizes fallback chrome classification, not managed-agent
// identity or runtime source facts. Keywords are case-insensitive substrings.
type GroupingConfig struct {
	AgentKeywords     []string `json:"agent_keywords"`
	ServiceKeywords   []string `json:"service_keywords"`
	DashboardKeywords []string `json:"dashboard_keywords"`
	ViewerKeywords    []string `json:"viewer_keywords"`
}

var defaultGrouping = GroupingConfig{
	AgentKeywords:     agentNameTokens,
	ServiceKeywords:   []string{"go2rtc", "frigate", "camera", "detector", "alerts", "notification-watcher"},
	DashboardKeywords: []string{"tmuxwatch", "dashboard", " mux "},
	ViewerKeywords:    []string{"-html", "localhost", "http://", "vite"},
}

func DefaultGroupingConfig() GroupingConfig {
	return GroupingConfig{AgentKeywords: slices.Clone(defaultGrouping.AgentKeywords), ServiceKeywords: slices.Clone(defaultGrouping.ServiceKeywords), DashboardKeywords: slices.Clone(defaultGrouping.DashboardKeywords), ViewerKeywords: slices.Clone(defaultGrouping.ViewerKeywords)}
}

func (m *Model) groupingConfig() GroupingConfig {
	if m != nil && m.grouping != nil {
		return *m.grouping
	}
	return defaultGrouping
}

func containsKeywords(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
