package ratchet

import (
	"fmt"
	"sort"
	"strings"
)

// UnknownMatcherKindError names a `[matcher].kind` this binary's compiled
// matcherKeys table does not recognize. ParseLaw returns it wrapped so a
// caller (LoadLaws) can tell "this ONE law names a rule I cannot run" apart
// from every other malformed-law failure, which still rejects the whole
// load: a typo in `severity` or a broken regex is a defect in the law file
// itself, but a kind this binary predates is a defect in the BINARY, on a
// box that has not rebuilt yet, and must not take every other law down with
// it. A consuming repo's laws can move ahead of a box's aphrollo binary —
// a lane lands a new matcher kind before every checkout is rebuilt from it
// — and the failure mode of a hard reject is loud and total: every commit
// in every checkout on the box, including lanes that never asked for the
// new kind.
type UnknownMatcherKindError struct {
	Kind MatcherKind
}

func (e *UnknownMatcherKindError) Error() string {
	return fmt.Sprintf("unknown matcher kind %q — known kinds: %s", e.Kind, knownKinds())
}

// knownKinds lists every matcher kind this binary compiles in, for the one
// error message that needs to say what IS supported alongside what is not.
func knownKinds() string {
	names := make([]string, 0, len(matcherKeys))
	for k := range matcherKeys {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
