package tdd

import (
	"slices"
	"testing"
)

// Issue #744: `gate mutants prove` ruled a mutation UNREADABLE over a red run
// whose failing test `aphrollo gate output` showed plainly on a FAIL line. The
// run was coloured: a repo that sets CARGO_TERM_COLOR=always (or a cargo
// `term.color = "always"`) makes nextest wrap every field of its status line in
// ANSI SGR sequences even when its stdout is a pipe, so the line starts with
// `ESC[31;1m        FAIL` instead of whitespace, and the name is split into
// three coloured spans. No line-anchored extractor matches that.
//
// The transcript is a REAL cargo-nextest 0.9.143 run, captured on this box with
// CARGO_TERM_COLOR=always from a crate whose inline `#[cfg(test)] mod tests`
// lives in src/aero_nodes.rs, under the mutation `(a + b) / 2.0` ->
// `(a + b) / 4.0`. Kept line for line; only the scratch directory is
// shortened. The name is the SAME one a monochrome run prints: the colour is
// presentation, and reading it has to be too.
const colouredNextestTranscript = "" +
	"\x1b[1m\x1b[92m   Compiling\x1b[0m forge_jbeam v0.1.0 (/tmp/fx/crates/forge_jbeam)\n" +
	"\x1b[1m\x1b[92m    Finished\x1b[0m `test` profile [unoptimized + debuginfo] target(s) in 0.14s\n" +
	"────────────\n" +
	"\x1b[32;1m Nextest run\x1b[0m ID \x1b[1mafeb77b0-b48c-410b-abd1-10ea23b67997\x1b[0m with nextest profile: \x1b[1mdefault\x1b[0m\n" +
	"\x1b[32;1m    Starting\x1b[0m \x1b[1m2\x1b[0m tests across \x1b[1m1\x1b[0m binary\n" +
	"\x1b[31;1m        FAIL\x1b[0m [   0.007s] (1/2) \x1b[35;1mforge_jbeam\x1b[0m \x1b[36maero_nodes::tests\x1b[0m\x1b[36m::\x1b[0m\x1b[34;1ma_node_shared_by_two_triangles_takes_their_area_weighted_mean_coefficient\x1b[0m\n" +
	"\x1b[31;1m \x1b[0m \x1b[31;1mstdout\x1b[0m \x1b[31;1m───\x1b[0m\n" +
	"\n" +
	"    running 1 test\n" +
	"    test aero_nodes::tests::a_node_shared_by_two_triangles_takes_their_area_weighted_mean_coefficient ... FAILED\n" +
	"\n" +
	"    failures:\n" +
	"\n" +
	"    failures:\n" +
	"        aero_nodes::tests::a_node_shared_by_two_triangles_takes_their_area_weighted_mean_coefficient\n" +
	"\n" +
	"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 1 filtered out; finished in 0.00s\n" +
	"    \x1b[0m\n" +
	"\x1b[31;1m \x1b[0m \x1b[31;1mstderr\x1b[0m \x1b[31;1m───\x1b[0m\n" +
	"\n" +
	"    \x1b[0m\x1b[31;1mthread 'aero_nodes::tests::a_node_shared_by_two_triangles_takes_their_area_weighted_mean_coefficient' (3895956) panicked at crates/forge_jbeam/src/aero_nodes.rs:11:9:\x1b[0m\n" +
	"    \x1b[31;1massertion `left == right` failed\x1b[0m\n" +
	"      left: 1.0\n" +
	"     right: 2.0\n" +
	"    note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\x1b[0m\n" +
	"\n" +
	"\x1b[31;1m  Cancelling\x1b[0m due to \x1b[31;1mtest failure\x1b[0m: \x1b[1m1\x1b[0m test still running\n" +
	"\x1b[32;1m        PASS\x1b[0m [   0.008s] (2/2) \x1b[35;1mforge_jbeam\x1b[0m \x1b[36maero_nodes::tests\x1b[0m\x1b[36m::\x1b[0m\x1b[34;1mother\x1b[0m\n" +
	"────────────\n" +
	"\x1b[31;1m     Summary\x1b[0m [   0.009s] \x1b[1m2\x1b[0m tests run: \x1b[1m1\x1b[0m \x1b[32;1mpassed\x1b[0m, \x1b[1m1\x1b[0m \x1b[31;1mfailed\x1b[0m, \x1b[1m0\x1b[0m \x1b[33;1mskipped\x1b[0m\n" +
	"\x1b[31;1m        FAIL\x1b[0m [   0.007s] (1/2) \x1b[35;1mforge_jbeam\x1b[0m \x1b[36maero_nodes::tests\x1b[0m\x1b[36m::\x1b[0m\x1b[34;1ma_node_shared_by_two_triangles_takes_their_area_weighted_mean_coefficient\x1b[0m\n" +
	"\x1b[31;1merror\x1b[0m: test run failed\n"

func TestExtractFailingTests_ReadsTheFailingNameThroughNextestColour(t *testing.T) {
	got := ExtractFailingTests(colouredNextestTranscript)
	want := []string{"aero_nodes::tests::a_node_shared_by_two_triangles_takes_their_area_weighted_mean_coefficient"}
	if !slices.Equal(got, want) {
		t.Fatalf("failing tests read from a coloured nextest run = %q, want %q", got, want)
	}
}
