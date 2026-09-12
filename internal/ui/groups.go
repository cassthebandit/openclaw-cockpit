package ui

type cockpitGroup struct {
	name string
	rank int
}

// Canonical group registry. Ranks and definitions mirror
// docs/group-registry.md exactly; that table is the source of truth.
var (
	// Rank 0 — live/resumable agent TUIs: managed agents in a live sub-state
	// (starting/running/waiting/blocked/review), plus prompt/approval screens
	// that can continue when answered.
	groupActiveAgents = cockpitGroup{name: "Active Agents", rank: 0}
	// Rank 1 — non-live agent panes blocked by an explicit hold, including the
	// held+marked conflict (hold always wins over a mark for presentation).
	groupHeldAgents = cockpitGroup{name: "Held / Teardown Blocked", rank: 1}
	// Rank 2 — sessions actually marked by the janitor with no hold/evidence
	// conflict; the only group that may render a cleanup countdown.
	groupMarkedForTeardown = cockpitGroup{name: "Marked For Teardown", rank: 2}
	// Rank 3 — non-active cleanup debt hygiene refuses to mark or kill
	// (missing/empty evidence, relative run root, invalid contract data).
	groupCleanupBlocked = cockpitGroup{name: "Cleanup Blocked", rank: 3}
	// Rank 4 — failed/problem agent panes inside the failure-visible window.
	groupFailedAgents = cockpitGroup{name: "Failed Agents", rank: 4}
	// Rank 5 — workflow/runtime failures that need operator judgment.
	groupOperationalFailures = cockpitGroup{name: "Operational Failures", rank: 5}
	// Rank 6 — platform, route, skeleton, or source-health failures.
	groupSubsystemFailures = cockpitGroup{name: "Sub-System Failures", rank: 6}
	// Rank 7 — healthy long-running watchers/bridges/monitors.
	groupServices = cockpitGroup{name: "Services", rank: 7}
	// Rank 8 — completed agent cards that are not held, failed-visible,
	// marked, or cleanup-blocked; also completed runtime cards.
	groupCompletedAgents = cockpitGroup{name: "Completed Agent Runs", rank: 8}
	// Rank 9+ — non-agent fallback work, self-monitoring UIs, viewers, shells.
	groupWork      = cockpitGroup{name: "Active Work", rank: 9}
	groupDashboard = cockpitGroup{name: "Dashboards", rank: 10}
	groupViewers   = cockpitGroup{name: "Viewers", rank: 11}
	groupIdle      = cockpitGroup{name: "Idle / Unowned", rank: 12}
)

// allCockpitGroups is the full canonical registry (docs/group-registry.md).
func allCockpitGroups() []cockpitGroup {
	return []cockpitGroup{
		groupActiveAgents,
		groupHeldAgents,
		groupMarkedForTeardown,
		groupCleanupBlocked,
		groupFailedAgents,
		groupOperationalFailures,
		groupSubsystemFailures,
		groupServices,
		groupCompletedAgents,
		groupWork,
		groupDashboard,
		groupViewers,
		groupIdle,
	}
}

// defaultExpandedGroups are the accordion sections that start expanded. Every
// other group defaults collapsed. Problem groups only appear when non-empty, so
// "expanded when non-empty" falls out of a plain expanded default.
var defaultExpandedGroups = map[string]struct{}{
	groupActiveAgents.name:        {},
	groupHeldAgents.name:          {},
	groupMarkedForTeardown.name:   {},
	groupCleanupBlocked.name:      {},
	groupFailedAgents.name:        {},
	groupOperationalFailures.name: {},
	groupSubsystemFailures.name:   {},
}
