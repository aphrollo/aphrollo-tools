package precommit

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"time"
)

// Why the merge gate ran a declared command instead of reusing a green. One
// row each; the line it prints is "[run] <cmd>: no reuse — <reason>".
const (
	missNoRecord = "no recorded verdict for this tree"
	missRed      = "the recorded verdict was red"
	missStore    = "the store could not be read"
	missOutside  = "a glob leaves the root"
	missUnhashed = "the inputs could not be hashed (%v)"
	missNoTool   = "the tool could not be found (%s)"
	missBadTool  = "the tool could not be read (%s): %v"
)

// The ways an input glob can leave a command without a key.
const (
	globNoMatch = iota
	globIgnored
	globOutside
)

// globRefusals say what each refusal is, and what its argument names.
var globRefusals = map[int]struct{ reason, error string }{
	globNoMatch: {"a glob matched no file (%s)", "input glob \"%s\" matches no file"},
	globIgnored: {"a glob selects an ignored file (%s)", "input glob %q selects the git-ignored file"},
	globOutside: {missOutside, "input glob \"%s\" starts outside the root"},
}

// globError is an input glob the gate refuses to key a command by; arg is the
// glob, or for globIgnored the ignored file it selects.
type globError struct {
	kind int
	arg  string
}

func (e *globError) Error() string {
	if e.kind == globIgnored {
		return fmt.Sprintf("input glob selects the git-ignored %s", e.arg)
	}
	return fmt.Sprintf(globRefusals[e.kind].error, e.arg)
}

// keyErrReason is the reason a command that takes part has no key.
func keyErrReason(err error) string {
	var g *globError
	var tool *toolError
	switch {
	case errors.As(err, &g):
		if g.kind == globOutside {
			return missOutside
		}
		return fmt.Sprintf(globRefusals[g.kind].reason, g.arg)
	case errors.As(err, &tool):
		if errors.Is(tool.err, exec.ErrNotFound) || errors.Is(tool.err, fs.ErrNotExist) {
			return fmt.Sprintf(missNoTool, tool.program)
		}
		return fmt.Sprintf(missBadTool, tool.program, tool.err)
	}
	return fmt.Sprintf(missUnhashed, err)
}

// declaredMissReasonAt is why the command c run in root has no green to
// reuse in the store at path, as of now.
func declaredMissReasonAt(path, root string, c declaredCommand, now time.Time) string {
	return missReasonFor(path, declaredKeying(root, c), now)
}

// declaredNewer reports whether entry a (under key ka) was written after b
// (under kb): by its time, then by its write sequence (two entries made in
// one second keep their order), then by key, so the answer never depends on
// map order.
func declaredNewer(ka string, a declaredVerdict, kb string, b declaredVerdict) bool {
	switch {
	case a.At != b.At:
		return a.At > b.At
	case a.Seq != b.Seq:
		return a.Seq > b.Seq
	}
	return ka > kb
}

// missAge is how long before now at was, in its largest whole unit.
func missAge(at, now time.Time) string {
	d := max(now.Sub(at), 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// missReasonFor is the reason the command keyed as k has no green in the
// store at path. A key that could not be taken, or a store that could not be
// read, is the reason. A red under this very key says so. Otherwise the parts
// of k are compared with those of the latest entry for the same command and
// root, and every one that moved is named, with the age of that entry.
func missReasonFor(path string, k declaredKeyed, now time.Time) string {
	if k.err != nil {
		return keyErrReason(k.err)
	}
	if path == "" {
		return missStore
	}
	s := loadDeclaredVerdicts(path)
	if s.newer || s.bad {
		return missStore
	}
	if v, found := s.Verdicts[k.key]; found && !v.Green {
		return missRed
	}
	lastKey, last := "", declaredVerdict{}
	for key, v := range s.Verdicts {
		if v.Scope != k.scope || v.Parts == nil {
			continue
		}
		if lastKey == "" || declaredNewer(key, v, lastKey, last) {
			lastKey, last = key, v
		}
	}
	if lastKey == "" {
		return missNoRecord
	}
	var moved []string
	for _, p := range []struct {
		name      string
		then, now string
	}{
		{"inputs", last.Parts.Inputs, k.parts.Inputs},
		{"lockfile/manifest", last.Parts.Locks, k.parts.Locks},
		{"tool", last.Parts.Tool, k.parts.Tool},
	} {
		if p.then != p.now {
			moved = append(moved, p.name)
		}
	}
	if len(moved) == 0 {
		return missNoRecord
	}
	age := "a while"
	if at, err := time.Parse(time.RFC3339, last.At); err == nil {
		age = missAge(at, now)
	}
	return fmt.Sprintf("%s changed since the last recorded run (%s ago)", joinAnd(moved), age)
}

// joinAnd is names as "a", "a and b" or "a, b and c".
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
