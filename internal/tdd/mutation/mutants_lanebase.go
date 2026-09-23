package mutation

// A lane's mutation run measures the diff from its base to HEAD, and the base
// has to be the newest trunk commit the lane ALREADY CONTAINS. Anything
// already merged in is, by definition, not what the lane is proposing: it
// passed its own gate and its own mutation run on trunk before it got there.
//
// Taking the first candidate ref that merely EXISTS cannot answer that. It
// prefers `origin/main`, and nothing in this package fetches, so that
// remote-tracking ref is whatever it last happened to be. A lane that catches
// up by merging its LOCAL main then resolves a base from before the merge, and
// the run charges it for every change trunk made in between — one real run
// measured 40 files and two crates the lane never opened (issue #261). The
// same stale ref refused commits outright in a repo whose main was hundreds of
// commits ahead of its last push: main's own unpushed work read as the lane's,
// and a baseline row main itself had written read as `0 -> 2006`.
//
// The gate now DRIVES lanes into that state: the push guard refuses a stale
// branch and prints a catch-up merge as the remedy, so a base that mishandles
// the catch-up is on the common path rather than the rare one.
