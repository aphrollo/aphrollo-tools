package postedit

import "strings"

// A crate's content-gated tests return early unless an environment switch
// points them at their content, and the gate's own runs never set it
// (issue #747). A test that returns early is counted as passed by libtest
// and nextest alike, so a green the gate logged for that crate cannot say
// that it skipped anything, and it is no answer for a run that sets the
// switch. The switches a repo declares for its fail-first proof
// (fail-first-env) are the ones it has said its suites are gated behind; a
// hand run that sets one of them is let through the rerun guard, and
// counted as its own escape.

// escapeBashEnvSwitch is the gate.log verdict a let-through run is counted
// under.
const escapeBashEnvSwitch = "override-bash-env-switch"

// setsDeclaredSwitch reports whether cmd sets, anywhere in it, one of the
// environment switches root's repo declares under fail-first-env: a
// `NAME=value` word (a leading assignment, an `export` or an `env` operand)
// or PowerShell's `$env:NAME`.
func setsDeclaredSwitch(root, cmd string) bool {
	declared := map[string]bool{}
	for _, kv := range readFailFirstEnv(root) {
		if name, _, ok := strings.Cut(kv, "="); ok {
			declared[strings.TrimSpace(name)] = true
		}
	}
	if len(declared) == 0 {
		return false
	}
	for _, words := range shellSegments(stripHeredocBodies(cmd)) {
		for _, w := range words {
			if declared[assignedEnvName(w)] {
				return true
			}
		}
	}
	return false
}

// assignedEnvName is the variable one shell word assigns, "" when it
// assigns none.
func assignedEnvName(w string) string {
	if strings.HasPrefix(strings.ToLower(w), "$env:") {
		name, _, _ := strings.Cut(w[len("$env:"):], "=")
		return name
	}
	if isEnvAssignment(w) {
		name, _, _ := strings.Cut(w, "=")
		return name
	}
	return ""
}
