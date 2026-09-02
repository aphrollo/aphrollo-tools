package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// withLinter states whether golangci-lint is installed, without rewriting
// PATH — on Windows that would also hide go and git from the very stages
// under test.
func withLinter(t *testing.T, present bool) {
	t.Helper()
	prev := lookLinter
	lookLinter = func() bool { return present }
	t.Cleanup(func() { lookLinter = prev })
}

// runsAt records every runner a gate executed at root, quality stages
// included — the point of these tests is exactly which CI-parity checks ran
// and in what order.
func runsAt(seen *[]Runner, root string) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		if dir == root {
			*seen = append(*seen, r)
		}
		return SuiteResult{Passed: true}
	}
}

func cmdLine(r Runner) string { return strings.TrimSpace(r.Cmd + " " + strings.Join(r.Args, " ")) }

// CI runs vet and the linter; a gate that does not runs a different check
// from the one that decides whether the branch is green. Order is the cost
// order: vet compiles nothing extra, lint is a full analysis pass, the suite
// builds and links.
func TestPrecommitGoRootRunsVetThenLintThenTheSuite(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, true)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	var order []string
	for _, r := range seen {
		order = append(order, cmdLine(r))
	}
	want := []string{"go vet ./...", "golangci-lint run ./...", "go test ."}
	if strings.Join(order, " | ") != strings.Join(want, " | ") {
		t.Fatalf("stages ran %v, want %v", order, want)
	}
}

// A linter nobody installed is not a failing commit. It is one log line, so
// the difference between "clean" and "never ran" stays visible.
func TestPrecommitSkipsTheLinterWhenItIsNotOnPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	withLinter(t, false)
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, runsAt(&seen, root))
	if res.Blocked {
		t.Fatalf("an absent linter must never reject a commit: %s", res.Message)
	}
	for _, r := range seen {
		if r.Cmd == "golangci-lint" {
			t.Fatalf("the linter is not installed, so it must not be run: %v", seen)
		}
	}
	requireLoggedVerdict(t, cfg, "lint-skipped")
}

// The linter that IS installed and finds something rejects, and the rejection
// names the command so it can be reproduced.
func TestPrecommitRejectsWhenTheLinterFails(t *testing.T) {
	root := makeGoRepo(t)
	withLinter(t, true)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(r Runner, dir string) SuiteResult {
		if r.Cmd == "golangci-lint" {
			return SuiteResult{Passed: false, Output: "widget.go:3:6: `Widget` is unused (unused)\n"}
		}
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("a lint failure must reject the commit")
	}
	for _, want := range []string{"golangci-lint run ./...", "Widget"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

// A dangling citation in a doc misdrives every session that loads it, and a
// docs-only commit stages no source at all — so the check has to run before
// the has-code gate, not inside a per-root suite stage.
func TestPrecommitChecksStagedMarkdownOnADocsOnlyCommit(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "NOTES.md", "see [the plan](docs/nowhere.md)\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	if !res.Blocked {
		t.Fatalf("a dangling citation must reject: %+v", res)
	}
	for _, want := range []string{"NOTES.md", "docs/nowhere.md"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
}

func TestPrecommitPassesStagedMarkdownThatResolves(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "docs/plan.md", "# plan\n")
	write(t, root, "NOTES.md", "see [the plan](docs/plan.md)\n")
	gitDo(t, root, "add", ".")

	if res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }); res.Blocked {
		t.Fatalf("a citation that resolves must pass: %s", res.Message)
	}
}

// A repo that is neither a Go module nor opted in never sees the stage: the
// doc conventions it enforces are not universal.
func TestPrecommitSkipsDocsCheckForARepoThatDidNotOptIn(t *testing.T) {
	root := makeJSRepo(t, `{"name":"x","scripts":{"test":"vitest run"}}`)
	write(t, root, "NOTES.md", "see [the plan](docs/nowhere.md)\n")
	gitDo(t, root, "add", ".")

	if res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }); res.Blocked {
		t.Fatalf("an opt-out repo must not be judged on doc citations: %s", res.Message)
	}

	// The marker opts it in, and then the same commit is refused.
	write(t, root, filepath.Join(".aphrollo", "docs-check"), "")
	if res := Precommit(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} }); !res.Blocked {
		t.Fatalf(".aphrollo/docs-check must turn the stage on: %+v", res)
	}
}
