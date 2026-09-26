package failfirst

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestNarrowFailFirstTests_GoRunFilterNamesTheStagedTests pins issue #567: the
// Go fail-first proof used to run the whole package owning the staged tests
// (`go test ./internal/x`), which for this repo's own internal/tdd is a
// 400-600s package suite executed to prove that one staged test goes RED. The
// staged files' `func Test` names are the proof's actual subject, so the
// narrowed argv must carry them as an anchored -run filter and run seconds
// instead of minutes.
func TestNarrowFailFirstTests_GoRunFilterNamesTheStagedTests(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.21\n")
	write(t, root, "internal/x/x.go", "package x\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "internal/x/alpha_test.go",
		"package x\n\nimport \"testing\"\n\nfunc TestAlpha_widgetIsOne(t *testing.T) {}\n")
	write(t, root, "internal/x/beta_test.go",
		"package x\n\nimport \"testing\"\n\nfunc TestBeta_widgetIsStillOne(t *testing.T) {}\n")

	goRunner := Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(goRunner, root, []string{"internal/x/beta_test.go", "internal/x/alpha_test.go"})
	want := Runner{Cmd: "go", Args: []string{"test", "./internal/x", "-run", "^(TestAlpha_widgetIsOne|TestBeta_widgetIsStillOne)$"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests (go) = %+v, want %+v", got, want)
	}
}

// TestNarrowFailFirstTests_GoWithoutATestFuncKeepsPackageScope pins the one
// outcome worse than a slow proof: a -run filter that matches NOTHING makes
// the proof pass vacuously. A staged test file the name scan finds no `func
// Test` in (a fuzz-only or generated file, an unreadable one) must fall back
// to the package-scoped run rather than emit an empty or partial filter.
func TestNarrowFailFirstTests_GoWithoutATestFuncKeepsPackageScope(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.21\n")
	write(t, root, "internal/x/x.go", "package x\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "internal/x/fuzz_test.go",
		"package x\n\nimport \"testing\"\n\nfunc FuzzWidget(f *testing.F) {}\n")

	goRunner := Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(goRunner, root, []string{"internal/x/fuzz_test.go"})
	want := Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests (go, no Test func) = %+v, want %+v", got, want)
	}
}

// TestGoRunFilter_AnchorsAndQuotesEveryName pins the filter's shape itself: an
// anchored alternation, each name regexp-quoted, so a name carrying regexp
// metacharacters can neither widen the filter to other tests nor break the
// pattern outright.
func TestGoRunFilter_AnchorsAndQuotesEveryName(t *testing.T) {
	got := goRunFilter([]string{"TestPlain", "Test.Meta|Alt"})
	want := `^(TestPlain|Test\.Meta\|Alt)$`
	if got != want {
		t.Fatalf("goRunFilter = %q, want %q", got, want)
	}
}

// TestFailFirstStage_LogsTheArgvItActuallyRan pins the second half of #567:
// the stage line and its gate.log entry printed the DETECTED profile command
// (`go test ./...`) whatever the run was narrowed to, which is what misled a
// reading of the log into thinking the proof ran the full suite. The line must
// record the argv the proof actually executed.
func TestFailFirstStage_LogsTheArgvItActuallyRan(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go",
		"package m\n\nimport \"testing\"\n\nfunc TestWidget_returnsOne(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	var ran Runner
	run := func(r Runner, _ string) SuiteResult {
		ran = r
		return SuiteResult{Passed: false, Output: "./widget_test.go:6:5: undefined: Widget"}
	}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"widget_test.go"}, []string{"widget.go"}, run)
	})
	if res.Blocked {
		t.Fatalf("expected a conclusive non-violation (red-proven), got blocked: %s", res.Message)
	}
	wantCmd := cmdString(ran)
	if !strings.Contains(wantCmd, "-run") {
		t.Fatalf("setup: the fail-first proof must have run a narrowed argv, got %q", wantCmd)
	}
	if !strings.Contains(stderr, wantCmd) {
		t.Fatalf("stage line must name the argv it ran (%q), got: %s", wantCmd, stderr)
	}
	logData, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	if !strings.Contains(string(logData), wantCmd) {
		t.Fatalf("gate.log must record the argv it ran (%q), got:\n%s", wantCmd, logData)
	}
}
