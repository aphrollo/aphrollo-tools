package failfirst

import (
	"strings"
	"testing"
)

// allSkippedPkgJSONLine is the GoTestJSON shape a suite gated behind an
// environment switch produces when the switch is unset: the test was
// SELECTED and ran, and the first thing it did was call t.Skip. go test's
// own package verdict for that is "ok" — Action "pass" — which is exactly
// why the fail-first stage used to read it as "your tests pass at HEAD".
const allSkippedPkgJSONLine = `{"Action":"run","Package":"example.com/m","Test":"TestGPUParity"}
{"Action":"output","Package":"example.com/m","Test":"TestGPUParity","Output":"    gpu_test.go:9: FORGE_GPU_TESTS unset\n"}
{"Action":"skip","Package":"example.com/m","Test":"TestGPUParity","Elapsed":0}
{"Action":"output","Package":"example.com/m","Output":"ok  \texample.com/m\t0.004s\n"}
{"Action":"pass","Package":"example.com/m","Elapsed":0.004}
`

// TestFailFirstStage_RefusesAProofWhoseTestsAllSelfSkipped pins issue #656.
// The proof ran the staged tests against HEAD and every one of them skipped
// itself — the suite is gated behind an env switch the hook's environment
// does not carry. That says NOTHING about HEAD: the tests neither went RED
// nor passed. The stage used to report it as "the new tests PASS against the
// pre-edit code (HEAD)" and block a correct commit with a false accusation.
func TestFailFirstStage_RefusesAProofWhoseTestsAllSelfSkipped(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "gpu.go", "package m\n\nfunc Parity() int { return 1 }\n")
	write(t, root, "gpu_test.go", "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestGPUParity(t *testing.T) {\n\tif os.Getenv(\"FORGE_GPU_TESTS\") == \"\" {\n\t\tt.Skip(\"FORGE_GPU_TESTS unset\")\n\t}\n\tif Parity() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	run := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: "ok  \texample.com/m\t0.004s\n", GoTestJSON: allSkippedPkgJSONLine}
	}

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"gpu_test.go"}, []string{"gpu.go"}, run)
	})
	if !res.Blocked {
		t.Fatalf("a proof whose tests all self-skipped measured nothing and must not land silently, got: %+v", res)
	}
	if strings.Contains(res.Message, "PASS against the pre-edit code") {
		t.Fatalf("a self-skipped test never passed at HEAD; the message must not say it did:\n%s", res.Message)
	}
	for _, want := range []string{"skip", "example.com/m", failFirstEnvKey} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("message must name %q (the skip, where, and the remedy):\n%s", want, res.Message)
		}
	}
	if !strings.Contains(stderr, AllTestsSkipped) {
		t.Fatalf("the stage line must carry the %s verdict, got: %s", AllTestsSkipped, stderr)
	}
	if strings.Contains(stderr, "red-proven") {
		t.Fatalf("an inconclusive proof must never be logged as red-proven, got: %s", stderr)
	}
	requireLoggedVerdict(t, cfg, AllTestsSkipped)
}

// TestSkippedOnlyNames_ReadsTheSkipEveryRunnerItCanRead is the detector
// itself, over the exact text each runner prints. The negative cases matter
// as much as the positives: a run where ANY test reached a real verdict
// proved something about HEAD and must stay out of this verdict entirely.
func TestSkippedOnlyNames_ReadsTheSkipEveryRunnerItCanRead(t *testing.T) {
	cargo := Runner{Cmd: "cargo", Args: []string{"test"}}
	nextest := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}
	pytest := Runner{Cmd: "pytest", Args: []string{"-q"}}
	goRunner := Runner{Cmd: "go", Args: []string{"test", "./..."}}

	cases := []struct {
		name   string
		runner Runner
		res    SuiteResult
		want   []string
	}{{
		name:   "cargo libtest: every selected test was #[ignore]d",
		runner: cargo,
		res: SuiteResult{Passed: true, Output: "" +
			"     Running tests/gpu_parity.rs (target/debug/deps/gpu_parity-1a2b)\n\n" +
			"running 1 test\ntest gpu_parity ... ignored\n\n" +
			"test result: ok. 0 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"},
		want: []string{"tests/gpu_parity.rs"},
	}, {
		name:   "cargo libtest: one test actually ran, so the run proved something",
		runner: cargo,
		res: SuiteResult{Passed: true, Output: "" +
			"     Running tests/gpu_parity.rs (target/debug/deps/gpu_parity-1a2b)\n\n" +
			"test result: ok. 1 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"},
		want: nil,
	}, {
		name:   "nextest: the whole selection was skipped",
		runner: nextest,
		res: SuiteResult{Passed: true, Output: "" +
			"    Starting 0 tests across 1 binaries (1 skipped)\n" +
			"------------\n" +
			"     Summary [   0.011s] 0 tests run: 0 passed, 1 skipped\n"},
		want: []string{"cargo nextest run"},
	}, {
		name:   "nextest: tests ran beside the skipped ones",
		runner: nextest,
		res:    SuiteResult{Passed: true, Output: "     Summary [   0.011s] 3 tests run: 3 passed, 1 skipped\n"},
		want:   nil,
	}, {
		name:   "pytest: every collected test skipped",
		runner: pytest,
		res:    SuiteResult{Passed: true, Output: "s\n1 skipped in 0.01s\n"},
		want:   []string{"pytest"},
	}, {
		name:   "pytest: a passing test beside the skip",
		runner: pytest,
		res:    SuiteResult{Passed: true, Output: ".s\n1 passed, 1 skipped in 0.01s\n"},
		want:   nil,
	}, {
		name:   "go: the package's only test skipped itself",
		runner: goRunner,
		res:    SuiteResult{Passed: true, GoTestJSON: allSkippedPkgJSONLine},
		want:   []string{"example.com/m"},
	}, {
		name:   "go: a package that executed nothing at all is vacuous, not skipped",
		runner: goRunner,
		res:    SuiteResult{Passed: true, GoTestJSON: vacuousPkgJSONLine},
		want:   nil,
	}, {
		name:   "zig: this gate cannot read a skip out of its output, so it claims none",
		runner: Runner{Cmd: "zig", Args: []string{"build", "test"}},
		res:    SuiteResult{Passed: true, Output: "1 skipped\n"},
		want:   nil,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := skippedOnlyNames(c.runner, c.res)
			if err != nil {
				t.Fatalf("skippedOnlyNames: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("skippedOnlyNames = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFailFirstViolationMessage_SaysWhenASkipIsInvisibleToIt is part 3's
// honest half. A test that returns early on its own — the commonest way a
// Rust GPU suite gates itself — is indistinguishable from a passing one in
// every runner's output, and a runner whose skips this gate cannot read at
// all (vitest, zig) is worse still. The violation message is the one place
// a misled author reads, so that limit is stated there rather than left for
// somebody to rediscover the way #656 was.
func TestFailFirstViolationMessage_SaysWhenASkipIsInvisibleToIt(t *testing.T) {
	readable := failFirstViolationMessage(Runner{Cmd: "cargo", Args: []string{"test"}})
	if !strings.Contains(readable, failFirstEnvKey) {
		t.Fatalf("the violation message must name the remedy for an env-gated suite:\n%s", readable)
	}
	if !strings.Contains(readable, "SELF-SKIPPED") {
		t.Fatalf("the violation message must raise the self-skip possibility:\n%s", readable)
	}
	blind := failFirstViolationMessage(Runner{Cmd: "zig", Args: []string{"build", "test"}})
	if !strings.Contains(blind, "cannot read a skip") || !strings.Contains(blind, "zig") {
		t.Fatalf("for a runner whose skips this gate cannot read, the message must say so by name:\n%s", blind)
	}
}
