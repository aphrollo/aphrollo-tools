package suite

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// This file answers one question for the suite guard: which project root does
// a Bash/PowerShell command's test run actually LAND in? runscope.go answers
// the other half — how WIDE that run is — and a refusal needs both: a verdict
// may only block a run it is at least as wide as, AND only one about the same
// checkout.
//
// The guard used to answer the checkout question with the session's working
// directory, which is not the same thing. The harness resets a session's cwd
// to the primary checkout between tool calls, so a run a session issued
// inside a lane worktree — `cd <lane> && cargo nextest run -p server` — was
// judged against the primary's root, and refused on a green another session
// had logged for the primary during an unrelated merge (issue #645). The
// lane's own suite had never run on the lane's commit, and one of its tests
// was red: the refusal held back precisely the run that was needed, and
// repeated for every narrowing the session tried.
//
// The parsing reuses the package's one command parser — shellSegmentsTokens,
// dropLeadingEnvAssignmentWords, cdTargetTilde, resolveAgainst,
// splitFlagValue, classifySuiteSegment — rather than growing a second.
// Anything it cannot resolve reads as "unknown" and answers no root at all:
// a guess at the session cwd would re-refuse a run in a tree nobody has a
// verdict for (issue #953).

// runnerDirFlags name a test runner's own way of moving the run out of the
// shell's working directory: `go test -C <dir>`, and cargo's `--manifest-path
// <path>` (a FILE, whose directory is the tree). They are read only off a
// segment that classifySuiteSegment already recognised as a runner — `git -C
// <dir>` scopes one git process and does NOT move the shell, so a suite
// invocation later in the same command line still runs where the session
// stands.
var runnerDirFlags = map[string]bool{"-C": true, "--manifest-path": true}

// manifestFileFlags are the subset of runnerDirFlags whose operand names a
// manifest file rather than a directory.
var manifestFileFlags = map[string]bool{"--manifest-path": true}

// effectiveRunRoot reports the project root the command's test invocations
// run in, given the working directory the session issued it from. It returns
// "" when neither the command nor the cwd resolves to a project at all, which
// the callers read as "nothing to be redundant against".
//
// Every invocation in the command line has to agree: a command line that runs
// suites in two different trees has no single root to judge. A run whose tree
// cannot be named at all — a `cd` into a variable, a directory that does not
// exist, a directory change this scanner does not follow — answers "" for the
// same reason: the gate holds no verdict for a tree it cannot name, and
// falling back to the session's cwd attributed a run in an independent clone
// to the primary checkout and refused it (issue #953). The fallback is kept
// only for a command that runs no suite this scanner recognises.
func effectiveRunRoot(cwd, cmd string) string {
	dirs := suiteRunDirs(cwd, cmd)
	if len(dirs) == 0 {
		if cwd == "" {
			return ""
		}
		return findRootFrom(cwd)
	}
	root := ""
	for i, dir := range dirs {
		if dir == "" || !runDirExists(dir) {
			return ""
		}
		r := findRootFrom(dir)
		if i == 0 {
			root = r
			continue
		}
		if !sameRoot(r, root) {
			return ""
		}
	}
	return root
}

// runDirExists reports whether dir names an existing directory. A path that does
// not exist has no checkout to be in, and walking up from it for a marker
// lands on whichever ancestor happens to hold one — the session's own
// checkout, for a relative path the scanner mis-joined onto the cwd.
func runDirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// suiteRunDirs walks the command line left to right, tracking the directory a
// shell would be in at each segment, and returns the directory each test
// runner invocation would start in — one entry per invocation, "" for one
// whose directory this scanner cannot resolve (a `cd` into a variable, a bare
// `cd`, a `--manifest-path` with no operand).
func suiteRunDirs(cwd, cmd string) []string {
	cur := cwd
	var dirs []string
	// outer holds the directory to restore at each open subshell's `)`.
	var outer []string
	for _, seg := range shellSegmentsTokens(stripHeredocBodies(cmd)) {
		inner, opened, closed := unwrapGroups(seg, len(outer))
		for i := 0; i < opened; i++ {
			outer = append(outer, cur)
		}
		cur = followSegment(cur, inner, &dirs)
		for i := 0; i < closed; i++ {
			cur = outer[len(outer)-1]
			outer = outer[:len(outer)-1]
		}
	}
	return dirs
}

// followSegment applies one segment (group syntax already stripped) to the
// tracked directory, appending to dirs the directory of a runner invocation
// in it, and returns the directory the shell is in afterwards.
func followSegment(cur string, seg []shellWord, dirs *[]string) string {
	trimmed := dropLeadingEnvAssignmentWords(seg)
	if target, isCd := cdTargetTilde(trimmed); isCd {
		return resolveRunDir(cur, target)
	}
	words := wordTexts(trimmed)
	if movesShellUnfollowed(words) {
		return ""
	}
	if classifySuiteSegment(words) != notSuiteInvocation {
		*dirs = append(*dirs, invocationDir(cur, words))
	}
	return cur
}

// movesShellUnfollowed reports whether a segment changes the shell's
// directory in a way this scanner does not model: `pushd` and `popd`. The
// directory after it is unknown, and an unknown one must never be read as the
// directory before it. A `cd` inside a subshell is followed and scoped to it
// (unwrapGroups); one inside a `{ …; }` group runs in the current shell and
// persists like any other.
func movesShellUnfollowed(words []string) bool {
	return len(words) > 0 && (words[0] == "pushd" || words[0] == "popd")
}

// pathStyleGOOS is the platform whose path spelling a run directory is read
// in; a variable so a test can spell Windows on any host.
var pathStyleGOOS = runtime.GOOS

// resolveRunDir is resolveAgainst after mapping an MSYS drive path.
func resolveRunDir(cwd, p string) string {
	return resolveAgainst(cwd, msysToWindows(pathStyleGOOS, p))
}

// msysToWindows maps a Git-Bash drive path (`/c/Users/x`, `/c`) to its
// Windows spelling (`C:\Users\x`, `C:\`) on goos "windows"; every other path,
// and every other platform, passes through. Without it a bash `cd /c/lane`
// resolves against the current drive's root and the run reads as a directory
// that does not exist.
func msysToWindows(goos, p string) string {
	if goos != "windows" || len(p) < 2 || p[0] != '/' || !isASCIILetter(p[1]) {
		return p
	}
	if len(p) == 2 {
		return strings.ToUpper(p[1:2]) + `:\`
	}
	if p[2] != '/' {
		return p
	}
	return strings.ToUpper(p[1:2]) + ":" + strings.ReplaceAll(p[2:], "/", `\`)
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// dropLeadingEnvAssignmentWords is dropLeadingEnvAssignments over a
// quote-aware shellWord segment: cdTargetTilde needs each word's raw half to
// tell a quoted `'~/x'` from an unquoted `~/x` (issue #867), so a `FOO=bar cd
// ~/lane` still finds `cd` at a fixed offset without losing that quoting.
func dropLeadingEnvAssignmentWords(words []shellWord) []shellWord {
	texts := wordTexts(words)
	i := 0
	for i < len(texts) && isEnvAssignment(texts[i]) {
		i++
	}
	return words[i:]
}

// invocationDir reports where one runner invocation starts: the directory the
// shell is standing in, unless the invocation names its own with a
// runnerDirFlags flag. A flag present but unresolvable answers "" — unknown —
// rather than silently falling back to the shell's directory, which is a
// different tree from the one the run was pointed at.
func invocationDir(cur string, words []string) string {
	for i := 0; i < len(words); i++ {
		name, val, inline := splitFlagValue(words[i])
		if !runnerDirFlags[name] {
			continue
		}
		if !inline {
			// The consumed index is dropped rather than skipped past: the
			// first directory flag is the answer, and this returns on it.
			val, _ = nextValue(words, i)
		}
		if val == "" {
			return ""
		}
		if manifestFileFlags[name] {
			val = filepath.Dir(filepath.FromSlash(val))
		}
		return resolveRunDir(cur, val)
	}
	return cur
}

// sameRoot reports whether two resolved project roots are the same tree.
// sameProject is this package's path comparison (cleaned, case-folded on
// Windows) and answers "the first is at or under the second", so asking it
// both ways is equality without a second spelling of the platform rules.
func sameRoot(a, b string) bool {
	return sameProject(a, b) && sameProject(b, a)
}
