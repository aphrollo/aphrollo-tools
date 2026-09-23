package tdd

import (
	"sort"
	"strings"
)

// A post-edit run is NARROWED on purpose, and that is the designed
// trade-off: a fast edit-time signal, with the mechanical suite at the
// merge. What went wrong is what a narrowed verdict was allowed to REFUSE. A
// crate whose substantive tests live in tests/integration/*.rs printed
// "green (1 passed, 1.2s)" off one unrelated inline test, and that line —
// settled, fresh — then silenced every hand-run suite for the tree for 30
// minutes (decideNarrowedSuite). A verdict about one module of one package
// was consumed as a verdict about the whole tree.
//
// The law this file implements: A VERDICT MAY ONLY BLOCK A RUN IT IS AT
// LEAST AS WIDE AS. The width is derived from the command gate.log already
// records, so nothing new has to be written to disk for it, and an
// unreadable command is never treated as wide enough — a false deny leaves a
// session with no way to get an answer, which is the failure this whole file
// exists to stop.

// runScope is the WIDTH of one test run: which packages it covers, and
// whether a target/name filter narrowed it further within them. whole means
// no package scoping at all — the entire project or workspace.
type runScope struct {
	whole   bool
	pkgs    map[string]bool
	filters []string
}

// cargoWholeScopeFlags widen a cargo run back to every member, whatever
// packages are also named.
var cargoWholeScopeFlags = map[string]bool{"--workspace": true, "--all": true}

// cargoScopeNeutralValueFlags consume a following bare token that is neither
// a package nor a filter, so it is not mistaken for cargo's positional name
// filter. An unlisted valued flag costs nothing worse than a scope that
// reads NARROWER than it is, which can only allow a rerun, never refuse one.
var cargoScopeNeutralValueFlags = map[string]bool{
	"-j": true, "--jobs": true, "--profile": true, "--target": true,
	"--features": true, "--test-threads": true, "--message-format": true,
	"--config": true, "--manifest-path": true, "--target-dir": true,
}

// goScopeFilterFlags name go test's own within-package narrowing.
var goScopeFilterFlags = map[string]bool{"-run": true, "-skip": true}

// goScopeNeutralValueFlags is goTestValueFlags' non-narrowing remainder,
// plus the ones a hand-run commonly carries.
var goScopeNeutralValueFlags = map[string]bool{
	"-timeout": true, "-count": true, "-cpu": true, "-parallel": true,
	"-tags": true, "-coverprofile": true, "-shuffle": true,
}

// scopeOfSuiteCommand reads ONE runner invocation's words — already
// quote-aware and heredoc-stripped when they come from a shell command,
// plain fields when they come from a gate.log line — and reports its width.
// false for anything that is not a runner this classifier knows, which
// callers must read as "cannot be judged", never as "whole".
func scopeOfSuiteCommand(words []string) (runScope, bool) {
	words = dropLeadingEnvAssignments(words)
	switch {
	case len(words) >= 2 && words[0] == "go" && words[1] == "test":
		return goRunScope(words[2:]), true
	case len(words) >= 2 && words[0] == "cargo" && words[1] == "test":
		return cargoRunScope(words[2:]), true
	case len(words) >= 3 && words[0] == "cargo" && words[1] == "nextest" && words[2] == "run":
		return cargoRunScope(words[3:]), true
	}
	return runScope{}, false
}

// cargoRunScope reads the words after `cargo test` / `cargo nextest run`.
// cargo's positional operand is always a name filter, never a package.
func cargoRunScope(args []string) runScope {
	s := runScope{pkgs: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		w := args[i]
		name, val, inline := splitFlagValue(w)
		switch {
		case cargoScopeValueFlags[name]:
			if !inline {
				val, i = nextValue(args, i)
			}
			if val != "" {
				s.pkgs[val] = true
			}
		case cargoWholeScopeFlags[name]:
			s.whole = true
		case cargoNarrowingValueFlags[name], cargoBuildOnlyFlags[name]:
			if !inline {
				val, i = nextValue(args, i)
			}
			s.filters = append(s.filters, name+" "+val)
		case cargoNarrowingBareFlags[name]:
			s.filters = append(s.filters, name)
		case strings.HasPrefix(w, "-"):
			if cargoScopeNeutralValueFlags[name] && !inline {
				_, i = nextValue(args, i)
			}
		default:
			s.filters = append(s.filters, w)
		}
	}
	return settleScope(s)
}

// goRunScope reads the words after `go test`. go's positional operand is a
// package pattern, and the "everything" markers are not a narrowing at all.
func goRunScope(args []string) runScope {
	s := runScope{pkgs: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		w := args[i]
		name, val, inline := splitFlagValue(w)
		switch {
		case goScopeFilterFlags[name]:
			if !inline {
				val, i = nextValue(args, i)
			}
			s.filters = append(s.filters, name+" "+val)
		case strings.HasPrefix(w, "-"):
			if goScopeNeutralValueFlags[name] && !inline {
				_, i = nextValue(args, i)
			}
		case wholeSuitePositionalMarkers[w]:
			s.whole = true
		default:
			s.pkgs[w] = true
		}
	}
	return settleScope(s)
}

// settleScope finishes a parsed scope: naming no package at all IS the whole
// project, and the filter list is sorted so two runs that named the same
// narrowings in a different order compare equal.
func settleScope(s runScope) runScope {
	if len(s.pkgs) == 0 {
		s.whole = true
	}
	sort.Strings(s.filters)
	return s
}

// splitFlagValue splits a `--flag=value` token; a bare token comes back as
// its own name with no inline value.
func splitFlagValue(w string) (name, val string, inline bool) {
	if i := strings.IndexByte(w, '='); i >= 0 {
		return w[:i], w[i+1:], true
	}
	return w, "", false
}

// nextValue takes the following bare token as a flag's value, returning the
// index it consumed so the caller's loop skips it. "" at the end of the
// argument list, which is a malformed command rather than a scope.
func nextValue(args []string, i int) (string, int) {
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", i
}

// scopeCovers reports whether a run of width have is AT LEAST AS WIDE as one
// of width want — the whole of the law. A run with no within-package filter
// answers for every package it named (and for everything, when it named
// none); a FILTERED run answers for exactly its own scope and nothing else,
// which is what keeps issue #572's catch intact: the same narrowed rerun,
// beside the same narrowed verdict, is still refused.
func scopeCovers(have, want runScope) bool {
	if len(have.filters) > 0 {
		return scopeEqual(have, want)
	}
	if have.whole {
		return true
	}
	if want.whole || len(want.pkgs) == 0 {
		return false
	}
	for p := range want.pkgs {
		if !have.pkgs[p] {
			return false
		}
	}
	return true
}

// scopeEqual reports whether two runs cover exactly the same ground.
func scopeEqual(a, b runScope) bool {
	if a.whole != b.whole || len(a.pkgs) != len(b.pkgs) {
		return false
	}
	for p := range a.pkgs {
		if !b.pkgs[p] {
			return false
		}
	}
	return strings.Join(a.filters, "\x00") == strings.Join(b.filters, "\x00")
}

// verdictCoversRun applies the law to a gate.log entry: the width of the run
// that produced the verdict, against the width of the run being attempted. A
// command this classifier cannot read (a wrapper script, a Makefile target,
// a stage that logged none) cannot be SHOWN to be wide enough, so it never
// refuses — the fail-open direction bashsuite.go owes.
func verdictCoversRun(e gateEntry, want runScope) bool {
	have, ok := scopeOfSuiteCommand(strings.Fields(e.Cmd))
	if !ok {
		return false
	}
	return scopeCovers(have, want)
}

// attemptedScope is the width of the run a shell command asks for: the
// widest suite invocation among its segments. Two segments of DIFFERENT
// scope merge into their union with the filters dropped — the wider reading,
// so a compound command is never refused on the strength of its narrowest
// half. A command naming no invocation this classifier reads comes back
// whole, so only a whole-tree verdict could refuse it.
func attemptedScope(cmd string) runScope {
	out := runScope{whole: true, pkgs: map[string]bool{}}
	found := false
	for _, words := range shellSegments(stripHeredocBodies(cmd)) {
		s, ok := scopeOfSuiteCommand(words)
		if !ok {
			continue
		}
		if !found {
			out, found = s, true
			continue
		}
		if !scopeEqual(out, s) {
			out = mergeScopes(out, s)
		}
	}
	return out
}

// mergeScopes is that union: every package either named, whole if either was
// whole, and no filters — a scope at least as wide as both.
func mergeScopes(a, b runScope) runScope {
	m := runScope{whole: a.whole || b.whole, pkgs: map[string]bool{}}
	for p := range a.pkgs {
		m.pkgs[p] = true
	}
	for p := range b.pkgs {
		m.pkgs[p] = true
	}
	return settleScope(m)
}

// wholeRunScope is the width of an un-narrowed invocation: the widest run a
// session can ask for, which only a whole-tree verdict answers.
func wholeRunScope() runScope {
	return runScope{whole: true, pkgs: map[string]bool{}}
}
