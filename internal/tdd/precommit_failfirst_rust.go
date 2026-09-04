package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rust's OWN idiomatic unit-test shape is a `#[test]` inside an inline
// `#[cfg(test)] mod tests { ... }` living in the SAME file as the code it
// exercises — src/widget.rs, never widget_test.rs. ClassifyFile therefore
// calls that file Source, never Test, so it never enters failFirstStage's
// `len(tests) > 0` branch and the single most common Rust TDD commit shape
// (a new function plus its `#[test]` in one file) got zero fail-first
// signal, with nothing on stderr or in gate.log to say so.
//
// This is unlike the Zig carve-out, which is a deliberate, documented
// language necessity: EVERY Zig file is both source and test by convention,
// with no separate-file alternative, and the smell/oracle gate reaches
// inline Zig tests through zigTestLines' block extraction. Rust also has
// real tests/ integration files, so the inline shape here is not a language
// necessity being worked around — it is just unhandled.
//
// Closing this properly needs the same block-extraction zigTestLines does:
// isolate the new #[test] fn's added lines from the source changes beside it
// in the same file's diff, so fail-first can apply "just the new test" onto
// HEAD without dragging the new production code along (which would make the
// test pass at HEAD and prove nothing). That is a real feature, not a
// one-line fix, and is left undone here. What this closes is the SILENCE:
// failFirstStageWithRustNotice names the gap on stderr and in gate.log,
// matching every other inconclusive path in this file, instead of a commit
// shaped this way leaving no trace that fail-first even looked at it.

// failFirstStageWithRustNotice runs failFirstStage, then — only when that
// call structurally could not fire — checks whether the reason is an inline
// Rust `#[cfg(test)]` unit test and, if so, logs the gap instead of staying
// silent.
func failFirstStageWithRustNotice(repoRoot, root string, tests, srcs []string, run SuiteRunner) GateResult {
	res := failFirstStage(repoRoot, root, tests, srcs, run)
	if res.Blocked {
		return res
	}
	if len(tests) > 0 && len(srcs) > 0 && stagedTestsAddDeclIn(repoRoot, tests) {
		return res // failFirstStage already ran and already logged
	}
	if !stagedSourceAddsInlineRustTest(repoRoot, srcs) {
		return res
	}
	line := fmt.Sprintf("gate precommit: fail-first in %s → inconclusive (rust inline #[cfg(test)] unit test — "+
		"fail-first cannot isolate it from its file's source changes in the same diff)", root)
	fmt.Fprintln(os.Stderr, line)
	appendGateLog("precommit", root, "", "inconclusive (rust-inline-test)", 0)
	return res
}

// stagedSourceAddsInlineRustTest reports whether any of srcFiles (a root's
// OWN staged Source-classified files) is a .rs file whose staged diff adds a
// line matching testDeclRes[".rs"] — the same declaration shape
// stagedTestsAddDeclIn looks for in a Test-classified file, applied here to
// Source-classified ones so the inline `#[cfg(test)]` shape is DETECTED even
// though fail-first cannot yet run against it.
func stagedSourceAddsInlineRustTest(repoRoot string, srcFiles []string) bool {
	want := map[string]bool{}
	for _, f := range srcFiles {
		if strings.EqualFold(filepath.Ext(f), ".rs") {
			want[f] = true
		}
	}
	if len(want) == 0 {
		return false
	}
	res := testDeclRes[".rs"]
	for _, fa := range stagedAdds(repoRoot) {
		if !want[fa.path] {
			continue
		}
		post, err := git(repoRoot, "show", ":"+fa.path)
		if err != nil {
			continue
		}
		lines := strings.Split(post, "\n")
		for no := range fa.added {
			if no < 1 || no > len(lines) {
				continue
			}
			for _, re := range res {
				if re.MatchString(lines[no-1]) {
					return true
				}
			}
		}
	}
	return false
}
