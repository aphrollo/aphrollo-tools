package tdd

import (
	"path/filepath"
	"strings"
)

// This file answers one question for the suite guard: which project root does
// a Bash/PowerShell command's test run actually LAND in?
//
// The guard used to answer it with the session's working directory, which is
// not the same thing. The harness resets a session's cwd to the primary
// checkout between tool calls, so a run a session issued inside a lane
// worktree — `cd <lane> && cargo nextest run -p server` — was judged against
// the primary's root, and refused on a green another session had logged for
// the primary during an unrelated merge (issue #645). The lane's own suite
// had never run on the lane's commit, and one of its tests was red: the
// refusal held back precisely the run that was needed, and repeated for every
// narrowing the session tried.
//
// Checkout identity and run width are two separate conditions, and BOTH have
// to hold before a rerun is redundant: the verdict must describe the same
// tree this command enters, and it must cover at least as much of it. This
// file owns the first; decideNarrowedSuite/decideWholeSuite own the second.
//
// The parsing reuses the package's one command parser — shellSegments,
// dropLeadingEnvAssignments, cdTarget, resolveAgainst, classifySuiteSegment —
// rather than growing a second. Anything it cannot resolve reads as "unknown"
// and falls back to the session cwd, which is today's behaviour: a guess
// would either waive the guard where it is right or re-refuse a run in a tree
// nobody has a verdict for.

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
// suites in two different trees has no single root to judge, and falls back
// to the cwd rather than picking one of them.
func effectiveRunRoot(cwd, cmd string) string {
	fallback := ""
	if cwd != "" {
		fallback = findRootFrom(cwd)
	}
	dirs := suiteRunDirs(cwd, cmd)
	if len(dirs) == 0 {
		return fallback
	}
	root := ""
	for i, dir := range dirs {
		if dir == "" {
			return fallback
		}
		r := findRootFrom(dir)
		if i == 0 {
			root = r
			continue
		}
		if !sameRoot(r, root) {
			return fallback
		}
	}
	return root
}

// suiteRunDirs walks the command line left to right, tracking the directory a
// shell would be in at each segment, and returns the directory each test
// runner invocation would start in — one entry per invocation, "" for one
// whose directory this scanner cannot resolve (a `cd` into a variable, a bare
// `cd`, a `--manifest-path` with no operand).
func suiteRunDirs(cwd, cmd string) []string {
	cur := cwd
	var dirs []string
	for _, seg := range shellSegments(stripHeredocBodies(cmd)) {
		words := dropLeadingEnvAssignments(seg)
		if target, isCd := cdTarget(words); isCd {
			cur = resolveAgainst(cur, target)
			continue
		}
		if classifySuiteSegment(words) == notSuiteInvocation {
			continue
		}
		dirs = append(dirs, invocationDir(cur, words))
	}
	return dirs
}

// invocationDir reports where one runner invocation starts: the directory the
// shell is standing in, unless the invocation names its own with a
// runnerDirFlags flag. A flag present but unresolvable answers "" — unknown —
// rather than silently falling back to the shell's directory, which is a
// different tree from the one the run was pointed at.
func invocationDir(cur string, words []string) string {
	for i := 0; i < len(words); i++ {
		name := flagName(words[i])
		if !runnerDirFlags[name] {
			continue
		}
		operand := ""
		switch {
		case strings.Contains(words[i], "="):
			operand = words[i][strings.IndexByte(words[i], '=')+1:]
		case i+1 < len(words):
			operand = words[i+1]
		}
		if operand == "" {
			return ""
		}
		if manifestFileFlags[name] {
			operand = filepath.Dir(filepath.FromSlash(operand))
		}
		return resolveAgainst(cur, operand)
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
