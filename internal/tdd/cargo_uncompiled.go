package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// NotCompiled is the verdict for an edited Rust test file that no `mod`
// declaration or cargo target reaches: cargo's own build never saw it, so a
// pass anywhere else in the crate says nothing about it. Inconclusive
// family, alongside BuildOnly/NoTestsSelected/InfraFailed — deliberately not
// a shape isSettledVerdict recognises, and never printed as green.
const NotCompiled = "not-compiled"

// rustModDeclNamed builds a regexp matching a `mod <name>;` declaration for
// the given name — bare or `#[path = "..."]`-mounted, `pub`/`pub(crate)` or
// not, with any attributes (`#[cfg(test)]`, a doc comment's `#[doc]` form)
// between the mount attribute and `mod`. An inline `mod name { … }` mounts no
// file and is deliberately not matched: cargoNestedTestFileReachable already
// treats an undeclared name as unreachable, which is the correct answer for
// an inline module too (it names no separate file at all).
func rustModDeclNamed(name string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?m)^\s*(?:#\[[^\]]*\]\s*)*(?:pub\s*(?:\([^)]*\)\s*)?)?mod\s+` + regexp.QuoteMeta(name) + `\s*;`)
}

// cargoNestedTestFileReachable reports whether a nested integration-test
// file — cargoTestTarget's "any OTHER file inside tests/<dir>/" case, folded
// into some test binary rather than being one itself — is actually reached
// by a `mod <stem>;` declaration in that directory's own mod.rs. The mod.rs
// (or main.rs) entry point itself is always reachable — it is what cargo's
// own directory-binary convention compiles regardless of what it declares
// about ITSELF.
//
// No mod.rs at all, or one that never names the file's own stem, means
// nothing in the crate's module tree mounts the file: cargo's build never
// reads it, whatever the rest of the package's tests report (issue #736).
func cargoNestedTestFileReachable(root, rel string) bool {
	base := filepath.Base(rel)
	if base == "mod.rs" || base == "main.rs" {
		return true
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	modPath := filepath.Join(root, filepath.FromSlash(filepath.Dir(rel)), "mod.rs")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return false
	}
	return rustModDeclNamed(stem).MatchString(string(data))
}

// notCompiledTerminal is buildOnlyTerminal's sibling for the other shape of
// "compiled nothing": a nested tests/<dir>/ file no mod declaration reaches
// at all. res.Passed is required — a run that failed for some unrelated
// reason stays a real red, not laundered into this inconclusive verdict; the
// point of this check is only to stop an UNRELATED pass from reading as
// evidence about a file the build never touched.
func notCompiledTerminal(r Runner, root, target string, res SuiteResult) string {
	if r.Cmd != "cargo" || !res.Passed {
		return ""
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if _, nested := cargoTestTarget(rel); !nested {
		return ""
	}
	if cargoNestedTestFileReachable(root, rel) {
		return ""
	}
	AppendGateLog("postedit", root, cmdString(r), NotCompiled, res.Duration)
	return notCompiledAdvisory(r, root, rel, res.Duration)
}

// notCompiledAdvisory is the one line that case prints.
func notCompiledAdvisory(r Runner, root, rel string, dur time.Duration) string {
	return fmt.Sprintf("gate: %s in %s → %s (%s) — %s is not declared by any mod/target that reaches it, so nothing about it was built or run — the code was NOT tested",
		cmdString(r), root, strings.ToUpper(NotCompiled), fmtSeconds(dur), rel)
}

// fmtSeconds renders a duration the way every other advisory line in this
// package does ("%.1fs"), extracted so notCompiledAdvisory reads the same as
// its siblings without importing fmt twice for one line.
func fmtSeconds(dur time.Duration) string {
	return fmt.Sprintf("%.1fs", dur.Seconds())
}
