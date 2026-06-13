package guardrail

import (
	"fmt"
	"regexp"
	"strings"
)

// quietRule flags a command that tends to flood the token stream and points at
// its lossless quiet form. Warnings are advisory; they never block.
type quietRule struct {
	match   *regexp.Regexp // command invokes this tool
	already *regexp.Regexp // a quiet flag is already present
	suggest string         // human-facing fix
}

var quietRules = []quietRule{
	{
		match:   regexp.MustCompile(`\bpytest\b`),
		already: regexp.MustCompile(`(^|\s)(-q|-qq|--quiet)(\s|$)`),
		suggest: "pytest is verbose by default; add -q to keep only the summary.",
	},
	{
		match:   regexp.MustCompile(`\bcargo\s+(build|test|check|run|clippy)\b`),
		already: regexp.MustCompile(`(^|\s)(-q|--quiet)(\s|$)`),
		suggest: "cargo is verbose; add -q to suppress progress output.",
	},
	{
		match:   regexp.MustCompile(`\bnpm\s+(install|ci|i|run)\b`),
		already: regexp.MustCompile(`(^|\s)(-s|--silent|--quiet|--no-progress)(\s|$)`),
		suggest: "npm prints a large progress/log wall; add --silent --no-progress.",
	},
	{
		match:   regexp.MustCompile(`\bpip3?\s+install\b`),
		already: regexp.MustCompile(`(^|\s)(-q|--quiet)(\s|$)`),
		suggest: "pip install is noisy; add -q to reduce output.",
	},
}

// outputBounded reports whether the command already caps its own output via a
// pipe or redirect, in which case a noisy-output warning would just be nagging.
var outputBounded = regexp.MustCompile(`[|>]`)

func checkUnboundedOutput(command string) (Decision, bool) {
	if outputBounded.MatchString(command) {
		return Decision{}, false
	}
	for _, r := range quietRules {
		if r.match.MatchString(command) && !r.already.MatchString(command) {
			return Decision{
				Action: Warn,
				Reason: fmt.Sprintf("%s Full output is preserved either way — this just trims token cost.", strings.TrimSpace(r.suggest)),
			}, true
		}
	}
	return Decision{}, false
}
