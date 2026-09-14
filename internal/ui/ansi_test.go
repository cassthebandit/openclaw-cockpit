package ui

import (
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// Frozen baseline oracle from 16b8ca9; intentionally independent of the scanner.
var (
	baselineCSI = regexp.MustCompile("\x1b\\[[0-9;:?]*[ -/]*[@-~]")
	baselineOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
)

func baselineStripANSI(s string) string {
	s = baselineCSI.ReplaceAllString(s, "")
	s = baselineOSC.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r == 0x1b || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ansiEdgeCases() []string {
	return []string{
		"", "plain\n\ttext", "\x1b", "\x1b[", "\x1b]unterminated", "\x1b[999;:? /~",
		"\x1b[>1m", "\x1b[12\n3m", "\x1b]abc\x1b[31mdef\a", "\x1b\x1b[0m]title\a",
		"\x1b]a\x1b]inner\aoutside", "\x1b]a\x1b\\tail", "\x1b]a\x1bX\a",
		"\xff\xfe\xc0\x80\xed\xa0\x80", "\x1b]\xff\a", "\x1b[\xffm",
		"│ › Sautéed 日本語 👩🏽‍💻\u009b\u009d\u202e", "\x00\r\b\v\f\x7f\t\n",
		codexIdleFinished, claudeIdleFinished, fableIdleFinished, fableBakedReadyForParentReview,
		fableSauteedIdleFinished, fableCrunchedIdleFinished, geminiIdleFinished, agyIdleFinished,
		codexStillWorking, claudeBackgroundRunning, runningNoCompletion, completionNoPrompt,
	}
}

func checkANSIEquivalent(t *testing.T, s string) {
	t.Helper()
	if got, want := stripANSI(s), baselineStripANSI(s); got != want {
		t.Fatalf("input %q\nscanner %q\nbaseline %q", s, got, want)
	}
}

func TestStripANSIDifferential(t *testing.T) {
	for _, s := range ansiEdgeCases() {
		checkANSIEquivalent(t, s)
		for i := range len(s) + 1 {
			checkANSIEquivalent(t, s[:i]) // every truncation, including inside UTF-8
		}
	}
	// Exhaust each byte at CSI/OSC grammar boundaries, including invalid UTF-8.
	for a := range 256 {
		for b := range 256 {
			checkANSIEquivalent(t, "\x1b["+string([]byte{byte(a), byte(b)})+"m\x1b]x\a")
			checkANSIEquivalent(t, "\x1b]"+string([]byte{byte(a), byte(b)})+"\x1b\\")
		}
	}
	rng := rand.New(rand.NewSource(63274))
	tokens := append(ansiEdgeCases(), "\x1b[31m", "\x1b[0m", "\x1b]", "\a", "\x1b\\", "[", "]", "\x1b")
	for range 10000 {
		var b strings.Builder
		for range rng.Intn(16) {
			b.WriteString(tokens[rng.Intn(len(tokens))])
		}
		checkANSIEquivalent(t, b.String())
	}
}

func FuzzStripANSIEquivalent(f *testing.F) {
	for _, s := range ansiEdgeCases() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { checkANSIEquivalent(t, s) })
}

func TestANSIFixtureLifecycleParity(t *testing.T) {
	fixtures := ansiEdgeCases()
	paths, err := filepath.Glob("../../helpers/tests/fixtures/*.ansi")
	if err != nil || len(paths) == 0 {
		t.Fatalf("saved terminal fixtures unavailable: %v", err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, string(data))
	}
	for _, fixture := range fixtures {
		for _, text := range []string{fixture, "\x1b[32m" + fixture + "\x1b[0m", "\x1b]title\a" + fixture} {
			checkANSIEquivalent(t, text)
			pane := tmux.Pane{PreviewText: text, Cockpit: &tmux.CockpitMeta{Kind: "agent", Agent: "fable", State: "running"}}
			got := paneLifecycleVerdictFor(pane, true)
			pane.PreviewText = baselineStripANSI(text)
			if want := paneLifecycleVerdictFor(pane, true); !reflect.DeepEqual(got, want) {
				t.Fatalf("fixture %q: scanner %#v, baseline %#v", text, got, want)
			}
		}
	}
}

func BenchmarkStripANSI(b *testing.B) {
	for name, body := range map[string]string{
		"ansi-heavy": benchPaneBody(600, "\x1b[38;5;108m* working...\x1b[0m esc to interrupt\n"),
		"plain":      baselineStripANSI(benchPaneBody(600, "working... esc to interrupt\n")),
		"osc":        strings.Repeat("\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\\n", 600),
	} {
		b.Run(name, func(b *testing.B) {
			for name, strip := range map[string]func(string) string{"baseline": baselineStripANSI, "scanner": stripANSI} {
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(body)))
					for range b.N {
						_ = strip(body)
					}
				})
			}
		})
	}
}
