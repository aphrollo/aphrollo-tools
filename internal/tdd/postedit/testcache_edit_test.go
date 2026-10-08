package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// test-cache = "edit" (or "commit") lets go's cache serve the post-edit suite;
// with it off the runner is the one the gate always ran.
func TestPostEdit_TestCacheSettingMarksTheRunnerCached(t *testing.T) {
	for _, c := range []struct {
		setting string
		want    bool
	}{{"", false}, {"off", false}, {"edit", true}, {"commit", true}} {
		t.Setenv("TRELLIS_DATA", t.TempDir())
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := mkProject(t, "go.mod")
		if c.setting != "" {
			body := "[aphrollo]\ntest-cache = \"" + c.setting + "\"\ntest-cache-impure = [\"./internal/git/...\"]\n"
			if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var seen Runner
		run := func(r Runner, _ string) SuiteResult {
			seen = r
			return SuiteResult{Passed: true, Output: "ok\nPASS"}
		}
		PostEdit(postPayload("Edit", filepath.Join(root, "widget.go")), run)
		if seen.Cmd != "go" {
			t.Fatalf("test-cache %q: the suite ran %q, want go", c.setting, seen.Cmd)
		}
		if seen.Cached != c.want {
			t.Errorf("test-cache %q: Cached = %v, want %v", c.setting, seen.Cached, c.want)
		}
		if c.want && len(seen.Impure) != 1 {
			t.Errorf("test-cache %q: Impure = %v, want the repo's list", c.setting, seen.Impure)
		}
	}
}

// A package go served from its cache is never shown as a fresh run: the label
// counts the tests and the cached packages apart.
func TestGreenLabel_NamesThePackagesServedFromTheCache(t *testing.T) {
	out := "--- PASS: TestA (0.00s)\nok  \texample.com/a\t(cached)\n--- PASS: TestB (0.00s)\nok  \texample.com/b\t0.150s\n"
	if got, want := greenLabel(Green, out, 1200*time.Millisecond), "green (2 passed (1 cached), 1.2s)"; got != want {
		t.Fatalf("greenLabel = %q, want %q", got, want)
	}
	fresh := "--- PASS: TestB (0.00s)\nok  \texample.com/b\t0.150s\n"
	if got := greenLabel(Green, fresh, 1200*time.Millisecond); strings.Contains(got, "cached") {
		t.Fatalf("a run with nothing cached says %q", got)
	}
}

// The unconstrained-green line states its count the same way: tests passed,
// and how many packages of them go served from its cache.
func TestUnconstrainedLine_NamesThePackagesServedFromTheCache(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "./x"}}
	if got := unconstrainedLine(r, "/r", 4, 1, time.Second); !strings.Contains(got, "(4 passed (1 cached); no test changed") {
		t.Fatalf("line = %q", got)
	}
	if got := unconstrainedLine(r, "/r", 4, 0, time.Second); !strings.Contains(got, "(4 passed; no test changed") {
		t.Fatalf("a run with nothing cached says %q", got)
	}
}
