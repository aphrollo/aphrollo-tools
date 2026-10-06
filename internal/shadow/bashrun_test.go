package shadow

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseBashRun_NamesTheRunnerAndTheDirectoryOfASuiteTheAgentStarted(t *testing.T) {
	cwd := filepath.FromSlash("/w/repo")
	in := func(rel string) string { return filepath.Join(cwd, filepath.FromSlash(rel)) }
	cases := []struct {
		cmd  string
		argv []string
		dir  string
	}{
		{"go test ./internal/lane/...", []string{"go", "test", "./internal/lane/..."}, cwd},
		{"CGO_ENABLED=0 go test -run TestX ./pkg", []string{"go", "test", "-run", "TestX", "./pkg"}, cwd},
		{"cargo nextest run -p engine", []string{"cargo", "nextest", "run", "-p", "engine"}, cwd},
		{"python -m pytest -q tests/test_a.py", []string{"python", "-m", "pytest", "-q", "tests/test_a.py"}, cwd},
		{"cd backend && pytest -q", []string{"pytest", "-q"}, in("backend")},
		{"uv run pytest", []string{"pytest"}, cwd},
		{"npx vitest run", []string{"npx", "vitest", "run"}, cwd},
		{"cd frontend; npm test", []string{"npm", "test"}, in("frontend")},
		{"pnpm test", []string{"pnpm", "test"}, cwd},
		{"time go test ./...", []string{"go", "test", "./..."}, cwd},
		{"git status && go test ./... && echo done", []string{"go", "test", "./..."}, cwd},
	}
	for _, c := range cases {
		got, ok := ParseBashRun(c.cmd, cwd)
		if !ok || !reflect.DeepEqual(got.Argv, c.argv) || got.Dir != c.dir {
			t.Errorf("ParseBashRun(%q) = %+v, %v, want argv %v in %s", c.cmd, got, ok, c.argv, c.dir)
		}
	}
}

func TestParseBashRun_ACommandWhoseExitIsNotASuitesIsNoRun(t *testing.T) {
	cwd := filepath.FromSlash("/w/repo")
	for _, cmd := range []string{
		"go test ./... | tail -5",     // the pipe's exit is tail's
		"go test ./a && go test ./b",  // one exit for two suites
		"go test -list . ./pkg",       // lists, runs nothing
		"go test -c ./pkg",            // builds, runs nothing
		"cargo test --no-run",         // builds, runs nothing
		"pytest --collect-only",       // collects, runs nothing
		"npm install",                 // not a test
		"go build ./...",              // not a test
		"echo go test",                // an argument, not a command
		"cd $DIR && go test ./...",    // a directory no one can name
		"pushd sub && go test ./... ", // a directory the parse does not follow
		"",                            // nothing
	} {
		if got, ok := ParseBashRun(cmd, cwd); ok {
			t.Errorf("ParseBashRun(%q) = %+v, want no run", cmd, got)
		}
	}
}

func TestBashResult_ReadsTheExitAndTheOutputOfARecordedPostToolUseAndFailure(t *testing.T) {
	cases := []struct {
		file   string
		ok     bool
		exit   int
		output string
		id     string
		ms     int64
	}{
		{"posttooluse_bash_go_green.json", true, 0, "ok  \texample.com/m/internal/lane\t0.012s\n", "toolu_go_green", 2310},
		{"posttoolusefailure_bash_go_red.json", true, 1, "--- FAIL: TestLane (0.00s)\n    lane_test.go:9: got 1, want 2\nFAIL\nFAIL\texample.com/m/internal/lane\t0.011s\nFAIL", "toolu_go_red", 1980},
		{"posttoolusefailure_bash_py_red.json", true, 1, "FAILED tests/test_service.py::test_a - assert 1 == 2\n1 failed in 0.02s", "toolu_py_red", 1100},
		{"posttooluse_bash_ts_green.json", true, 0, " Test Files  1 passed (1)\n", "toolu_ts_green", 3100},
		{"posttoolusefailure_bash_interrupt.json", false, 0, "", "", 0},
		{"pretooluse_bash_write.json", false, 0, "", "", 0},
	}
	for _, c := range cases {
		got, ok := recorded(t, c.file).BashResult()
		if ok != c.ok || got.Exit != c.exit || got.Output != c.output || got.ToolUseID != c.id || got.DurationMS != c.ms {
			t.Errorf("%s: BashResult = %+v, %v, want exit %d, id %s, %dms, ok=%v", c.file, got, ok, c.exit, c.id, c.ms, c.ok)
		}
	}
}
