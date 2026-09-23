package suite

import (
	"strings"
)

// bashSuiteVerdictFreshFor bounds how long a logged green/red/blocked
// verdict speaks for a tree before a whole-suite rerun stops being redundant
// against it. Reusing redGoesStaleAfter rather than a second constant: both
// answer the same question — does an outcome logged a while ago still
// describe the tree a session is standing in right now.
const bashSuiteVerdictFreshFor = redGoesStaleAfter

var cargoNextestNarrowingFlags = map[string]bool{
	"-p": true, "--package": true, "-E": true, "--filter-expr": true,
}

var cargoNextestValueFlags = map[string]bool{
	"-p": true, "--package": true, "-E": true, "--filter-expr": true,
	"--manifest-path": true,
}

var cargoTestNarrowingFlags = map[string]bool{"-p": true, "--package": true}

// `--manifest-path <file>` names the tree, not a narrowing, and its operand
// is consumed for the same reason `-C`'s is: cargo reads any positional as a
// filter substring, so an unconsumed path would read as one.
var cargoTestValueFlags = map[string]bool{"-p": true, "--package": true, "--manifest-path": true}

// classifyRunnerArgs reads a runner's own arguments (the words after `go
// test` / `cargo test` / `cargo nextest run`) and decides whether they name a
// narrowing: an explicit narrowing flag, or a positional operand that is not
// one of go test's own "everything" markers (cargo's positional is always a
// filter substring/expression, so any positional at all narrows there).
func classifyRunnerArgs(args []string, valueFlags, narrowingFlags map[string]bool) suiteShape {
	narrowed := false
	skipNext := false
	for _, w := range args {
		if skipNext {
			skipNext = false
			continue
		}
		name := flagName(w)
		switch {
		case narrowingFlags[name]:
			narrowed = true
			if valueFlags[name] && !strings.Contains(w, "=") {
				skipNext = true
			}
		case valueFlags[name]:
			if !strings.Contains(w, "=") {
				skipNext = true
			}
		case strings.HasPrefix(w, "-"):
			// an ordinary flag, not a narrowing by itself.
		default:
			if !wholeSuitePositionalMarkers[w] {
				narrowed = true
			}
		}
	}
	if narrowed {
		return narrowedSuiteInvocation
	}
	return wholeSuiteInvocation
}

// classifySuiteSegment reads one shellSegments() word list — already
// quote-aware and heredoc-stripped — and reports whether it invokes go
// test / cargo test / cargo nextest run, and if so, whether it names a
// narrowing. Anything else (an alias, a Makefile target, a wrapper script)
// is outside a text classifier's reach and reads as notSuiteInvocation: the
// command may still run a whole suite, but this hook cannot see it, and
// missing one is the fail-open direction it owes.
func classifySuiteSegment(words []string) suiteShape {
	words = dropLeadingEnvAssignments(words)
	switch {
	case len(words) >= 2 && words[0] == "go" && words[1] == "test":
		return classifyRunnerArgs(words[2:], goTestValueFlags, goTestNarrowingFlags)
	case len(words) >= 2 && words[0] == "cargo" && words[1] == "test":
		return classifyRunnerArgs(words[2:], cargoTestValueFlags, cargoTestNarrowingFlags)
	case len(words) >= 3 && words[0] == "cargo" && words[1] == "nextest" && words[2] == "run":
		return classifyRunnerArgs(words[3:], cargoNextestValueFlags, cargoNextestNarrowingFlags)
	default:
		return notSuiteInvocation
	}
}

// dropLeadingEnvAssignments strips `FOO=bar` prefixes off a segment's words
// (`SOAK_SECS=600 go test ./...`), so the runner name is found at a fixed
// offset regardless of how many precede it.
func dropLeadingEnvAssignments(words []string) []string {
	i := 0
	for i < len(words) && isEnvAssignment(words[i]) {
		i++
	}
	return words[i:]
}

// flagName strips a `--flag=value` token down to `--flag`, so it can be
// looked up in valueFlags/narrowingFlags regardless of which form a command
// used.
func flagName(w string) string {
	if i := strings.IndexByte(w, '='); i >= 0 {
		return w[:i]
	}
	return w
}

// goTestNarrowingFlags name go test's own narrowing switch; a single named
// package is recognised separately, as a positional operand.
var goTestNarrowingFlags = map[string]bool{"-run": true}

// goTestValueFlags consume a following bare token as their value (`-run X`,
// not `-run=X`) so it is never mistaken for a positional package operand.
// `-C <dir>` is listed so its operand is consumed as the directory it is,
// never read as a positional package operand (which would classify a whole
// `go test -C <dir> ./...` as narrowed); runnerDir reads the same operand to
// find where the run lands.
var goTestValueFlags = map[string]bool{
	"-run": true, "-timeout": true, "-count": true, "-cpu": true, "-C": true,
}

// isEnvAssignment reports whether tok has the shape `NAME=...` with an
// identifier-shaped NAME — the one shell shape this file needs to recognise
// and skip past, not a general assignment parser.
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range tok[:eq] {
		isNameRune := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !isNameRune {
			return false
		}
	}
	return true
}

const (
	// notSuiteInvocation: no segment of the command runs go test / cargo
	// test / cargo nextest run at all — nothing here is this hook's concern.
	notSuiteInvocation suiteShape = iota
	// narrowedSuiteInvocation: a -run/-p/filter/single-package narrowing is
	// present. This is also the shape a hand-run mutation proof takes (one
	// targeted test, mutate, rerun) — the two are not distinguished, because
	// a mutation proof IS a narrowed invocation by construction.
	narrowedSuiteInvocation
	// wholeSuiteInvocation: the runner would exercise the whole project (or
	// whole workspace), no filter, no package, no -run.
	wholeSuiteInvocation
)

// suiteShape is what one Bash/PowerShell command asks the gate to judge.
type suiteShape int

// wholeSuitePositionalMarkers are the positional operands go test reads as
// "everything", never as a narrowing.
var wholeSuitePositionalMarkers = map[string]bool{"./...": true, "...": true, "./": true}
