package tddtest

import (
	"path/filepath"
	"strings"
	"time"
)

// ExampleCompiledOutput is what a build-only target's own run prints: the
// target was compiled, and no test summary appears anywhere because none
// ran.
const ExampleCompiledOutput = "   Compiling engine_audio v0.1.0 (/w/engine_audio)\n" +
	"    Finished `test` profile [unoptimized + debuginfo] target(s) in 1.42s\n"

// LaneSource is the lane commit every judgement test makes: one changed crate
// source, so there is something mutable to measure.
var LaneSource = map[string]string{"crates/a/src/lib.rs": "pub fn add(a: i32, b: i32) -> i32 { a - b }\n"}

// LedgerWidgetFixed is the widget source with its test and a fixed body.
const LedgerWidgetFixed = "pub fn widget() -> i32 { 2 }\n\n" +
	"#[cfg(test)]\nmod tests {\n    use super::*;\n\n" +
	"    #[test]\n    fn widget_doubles() {\n        assert_eq!(widget(), 2);\n    }\n}\n"

// LedgerWidgetImpl is the widget source with production code only.
const LedgerWidgetImpl = "pub fn widget() -> i32 { 1 }\n"

// LedgerWidgetWithTest is the widget source with a test that fails on it.
const LedgerWidgetWithTest = "pub fn widget() -> i32 { 1 }\n\n" +
	"#[cfg(test)]\nmod tests {\n    use super::*;\n\n" +
	"    #[test]\n    fn widget_doubles() {\n        assert_eq!(widget(), 2);\n    }\n}\n"

// NextestNoTestsOutput is VERBATIM cargo-nextest output, captured 2026-08-15
// (review finding: the fixture had been hand-written; this replaces it with
// a real capture) via:
//
//	cargo new --lib zz_empty && cd zz_empty
//	# src/lib.rs stripped of its default #[test] so the crate has ZERO tests
//	cargo nextest run
//
// which exits 4 (confirmed: `echo $?` => 4) with this transcript — the exact
// shape a cargo-hakari workspace-hack crate (deliberately dependency-only)
// produces on every commit that touches it, unlike plain `cargo test` (also
// captured, same zero-test crate: exit 0, "running 0 tests" /
// "test result: ok. 0 passed; 0 failed; ..." — already covered by
// ClassifyOutcome's existing zeroTestsRe, so no fixture change needed there).
const NextestNoTestsOutput = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.01s\n" +
	"────────────\n" +
	" Nextest run ID 2c75af04-dbe9-4f97-aa96-b51d23e40db1 with nextest profile: default\n" +
	"    Starting 0 tests across 1 binary\n" +
	"────────────\n" +
	"     Summary [   0.000s] 0 tests run: 0 passed, 0 skipped\n" +
	"error: no tests to run\n" +
	"(hint: use `--no-tests` to customize)\n"

// NextestSixPassedOutput is the widened run's own transcript: the package's
// tests DO exist (six of them, in a second binary — the integration target),
// they were simply not where the module filter looked.
const NextestSixPassedOutput = "    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.02s\n" +
	"    Starting 6 tests across 2 binaries\n" +
	"        PASS [   0.004s] engine_audio::intake intake_accepts_a_stream\n" +
	"     Summary [   0.012s] 6 tests run: 6 passed, 0 skipped\n"

// PrecommitTestTimeout bounds the real `go test` runs in these integration
// tests.
const PrecommitTestTimeout = 120 * time.Second

// TimeoutLoadFixtureLeaf and CheckStageLoadFixtureLeaf are the leaves of the
// two chains that get classified against the REAL test process's own pid
// (foreignLoadReport passes os.Getpid()), so they are the two that must live
// above maxAssignableOSPID — see
// TestForeignChainSample_CannotBeClaimedByTheProcessRunningIt.
// Both sit a clear order of magnitude above that ceiling, so the chains they
// build (leaf + maxAncestryDepth + 1 pids) stay out of reach of any live
// process on any runner.
const (
	SyntheticPIDBase          = 1 << 30
	TimeoutLoadFixtureLeaf    = SyntheticPIDBase + 999
	CheckStageLoadFixtureLeaf = SyntheticPIDBase + 42
)

// TempEnvKeys are the temp-dir variables goTmpEnv redirects for a `go`
// runner: GOTMPDIR is what the go tool itself reads when it stages a compiled
// test binary, and TMPDIR/TMP/TEMP are what an os.TempDir() call reads on the
// two platform families. Named once so every test of that redirect asserts
// over the same set — the pass-through half used to check only two of the four,
// which left a redirect of TMP or TEMP alone unguarded.
var TempEnvKeys = []string{"GOTMPDIR", "TMPDIR", "TMP", "TEMP"}

// VacuousPkgJSONLine is the GoTestJSON shape a stub SuiteRunner hands the
// gate for the #194 bug: the package built and the binary exited 0, but
// nothing behind it ran.
const VacuousPkgJSONLine = `{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

// BigFileLines builds a Go file of n lines: a package clause, then filler
// comments, so a law counting LINES has something to count and `go vet` still
// reads it as valid Go.
func BigFileLines(pkg string, head, tail int) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n")
	for i := range head {
		b.WriteString("// head " + string(rune('a'+i)) + "\n")
	}
	b.WriteString("\nfunc Middle() {}\n\n")
	for i := range tail {
		b.WriteString("// tail " + string(rune('a'+i)) + "\n")
	}
	return b.String()
}

// GateLines renders gate.log lines at ts, in the exact shape appendGateLog
// writes: the parser reads by field position, so a hand-built line that
// drifts from the writer would test the wrong thing.
func GateLines(ts time.Time, entries ...string) string {
	var b strings.Builder
	for i, e := range entries {
		b.WriteString(ts.Add(time.Duration(i) * time.Minute).Format(time.RFC3339))
		b.WriteString(" " + e + " 0.0s\n")
	}
	return b.String()
}

// FirstBytes is s cut to at most n bytes.
func FirstBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Stamp writes one gate.log line in the format appendGateLog produces;
// formatSecs is the package's own seconds formatter.
func Stamp(at time.Time, stage, root, cmd, verdict string, secs float64, formatSecs func(float64) string) string {
	return at.UTC().Format(time.RFC3339) + " " + stage + " " + root + " " + cmd + " " + verdict + " " +
		formatSecs(secs) + "s\n"
}

// SkillPath is where the tdd skill lives under a config dir.
func SkillPath(dir string) string {
	return filepath.Join(dir, "skills", "tdd", "SKILL.md")
}
