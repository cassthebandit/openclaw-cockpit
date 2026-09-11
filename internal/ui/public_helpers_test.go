package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publicFixtureProducer(t *testing.T, failure bool) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "helpers", "openclaw_runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cockpit_snapshot.py")); err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf("['--tasks-json', %q, '--flows-json', %q, '--audit-json', %q, '--include-stale']", filepath.Join(root, "tests", "fixtures", "tasks.json"), filepath.Join(root, "tests", "fixtures", "flows.json"), filepath.Join(root, "tests", "fixtures", "audit.json"))
	if failure {
		args = fmt.Sprintf("['--state-db', %q]", filepath.Join(t.TempDir(), "absent.sqlite"))
	}
	script := filepath.Join(t.TempDir(), "fixture.py")
	content := fmt.Sprintf("import sys\nsys.path.insert(0, %q)\nimport cockpit_snapshot\nraise SystemExit(cockpit_snapshot.main(%s + sys.argv[1:]))\n", root, args)
	if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestPublicHelperProducerConsumerContract(t *testing.T) {
	cards, err := loadOpenClawRuntimeCards(RuntimeSource{Enabled: true, Script: publicFixtureProducer(t, false), Limit: 1, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("delivered %d cards, want 1", len(cards))
	}
	if cards[0].summary.VisibleRuntimeCardCount != len(cards) {
		t.Fatalf("shown count does not match delivered cards: %+v", cards[0].summary)
	}
	if cards[0].summary.TotalVisibleRuntimeCardCount < len(cards) {
		t.Fatal("producer total is smaller than delivered count")
	}
}

func TestPublicHelperErrorReachesConsumer(t *testing.T) {
	_, err := loadOpenClawRuntimeCards(RuntimeSource{Enabled: true, Script: publicFixtureProducer(t, true), Limit: 1, Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "absent.sqlite") {
		t.Fatalf("actual public producer diagnostic lost: %v", err)
	}
}
