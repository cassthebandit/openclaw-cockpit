// File types_test.go validates helper methods on tmux structs.
package tmux

import "testing"

// TestPaneTitleOrCmd ensures titles and commands format as expected.
func TestPaneTitleOrCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pane Pane
		want string
	}{
		{
			name: "prefers title",
			pane: Pane{Title: "  logs  ", CurrentCmd: "bash"},
			want: "logs",
		},
		{
			name: "falls back to command",
			pane: Pane{CurrentCmd: "  go  "},
			want: "go",
		},
		{
			name: "defaults to pane",
			pane: Pane{},
			want: "pane",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.pane.TitleOrCmd(); got != tt.want {
				t.Fatalf("TitleOrCmd() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPaneStatusString ensures pane status strings match dead state.
func TestPaneStatusString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pane Pane
		want string
	}{
		{name: "running", pane: Pane{Dead: false}, want: "running"},
		{name: "exit 0", pane: Pane{Dead: true, DeadStatus: 0}, want: "exit 0"},
		{name: "exit code", pane: Pane{Dead: true, DeadStatus: 3}, want: "exit 3"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.pane.StatusString(); got != tt.want {
				t.Fatalf("StatusString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCockpitMetaHasData(t *testing.T) {
	t.Parallel()

	if (CockpitMeta{}).HasData() {
		t.Fatal("empty cockpit metadata should report no data")
	}
	if !(CockpitMeta{State: "running"}).HasData() {
		t.Fatal("populated cockpit metadata should report data")
	}
	if !(CockpitMeta{ProgressPath: "progress.jsonl"}).HasData() {
		t.Fatal("new cockpit metadata fields should report data")
	}
	if !(CockpitMeta{PresentationGroup: "route_health"}).HasData() {
		t.Fatal("presentation metadata fields should report data")
	}
}

func TestCockpitMetaDisplayOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta CockpitMeta
		want bool
	}{
		{name: "contract marker", meta: CockpitMeta{ContractVersion: "display-only"}, want: true},
		{name: "manual adopt marker", meta: CockpitMeta{ManagedBy: "manual_adopt"}, want: true},
		{name: "managed contract", meta: CockpitMeta{ContractVersion: "1", ManagedBy: "agent_wall"}, want: false},
		{name: "empty", meta: CockpitMeta{}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.meta.DisplayOnly(); got != tt.want {
				t.Fatalf("DisplayOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}
