package precommit

import (
	"errors"
	"fmt"
)

// Why the merge gate ran a declared command instead of reusing a green. One
// row each; the line it prints is "[run] <cmd>: no reuse — <reason>".
const (
	missNoRecord = "no recorded verdict for this tree"
	missRed      = "the recorded verdict was red"
	missInputs   = "inputs changed"
	missLocks    = "lockfile/manifest changed"
	missTool     = "tool changed"
	missStore    = "the store could not be read"
	missOutside  = "a glob leaves the root"
	missUnhashed = "the inputs could not be hashed (%v)"
	missNoTool   = "the tool could not be found (%s)"
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
		return fmt.Sprintf(missNoTool, tool.program)
	}
	return fmt.Sprintf(missUnhashed, err)
}

// declaredMissReasonAt is why the command c run in root has no green to
// reuse in the store at path.
func declaredMissReasonAt(path, root string, c declaredCommand) string {
	return missReasonFor(path, declaredKeying(root, c))
}

// missReasonFor is the reason the command keyed as k has no green in the
// store at path. A key that could not be taken, or a store that could not be
// read, is the reason. A red under this very key says so. Otherwise the parts
// of k are compared with those of the latest entry for the same command and
// root, and the first that moved is named.
func missReasonFor(path string, k declaredKeyed) string {
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
	var last *declaredVerdict
	for _, v := range s.Verdicts {
		if v.Scope != k.scope || v.Parts == nil {
			continue
		}
		if last == nil || v.At > last.At {
			v := v
			last = &v
		}
	}
	switch {
	case last == nil:
		return missNoRecord
	case last.Parts.Inputs != k.parts.Inputs:
		return missInputs
	case last.Parts.Locks != k.parts.Locks:
		return missLocks
	case last.Parts.Tool != k.parts.Tool:
		return missTool
	}
	return missNoRecord
}
