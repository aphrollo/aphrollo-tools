// Package guardrail implements the deterministic policy evaluated by the
// aphrollo PreToolUse hook for coder/devops sessions. Every decision is VISIBLE:
// blocks and warnings carry a reason plus a concrete fix suggestion; nothing is
// silently transformed.
package guardrail

import (
	"fmt"
	"regexp"
	"strconv"
)

// Action is the outcome of evaluating a tool call.
type Action int

const (
	Allow Action = iota
	Warn
	Block
)

func (a Action) String() string {
	switch a {
	case Warn:
		return "warn"
	case Block:
		return "block"
	default:
		return "allow"
	}
}

// Decision is the guardrail's verdict for one tool call.
type Decision struct {
	Action Action
	Reason string
}

// blockSleepAtSeconds is the threshold at or above which a foreground
// sleep/wait is blocked. A dispatch turn should not idle for seconds at a time;
// brief sub-2s pacing is tolerated. Matches Claude Code's own Bash tool, which
// blocks sleep >= 2s and pushes longer waits to background/poll.
const blockSleepAtSeconds = 2

var sleepRe = regexp.MustCompile(`\bsleep\s+([0-9]+(?:\.[0-9]+)?)([smhd]?)\b`)

// Evaluate applies the guardrail policy to a tool call. Only Bash commands are
// inspected; everything else is allowed.
func Evaluate(toolName, command string) Decision {
	if toolName != "Bash" {
		return Decision{Action: Allow}
	}
	// Match against a masked copy so a tool name or `sleep` inside a quoted
	// string or comment never triggers the policy.
	masked := mask(command)
	if d, hit := checkBlockingWait(masked); hit {
		return d
	}
	if d, hit := checkUnboundedOutput(masked); hit {
		return d
	}
	return Decision{Action: Allow}
}

func checkBlockingWait(command string) (Decision, bool) {
	for _, m := range sleepRe.FindAllStringSubmatch(command, -1) {
		secs := durationSeconds(m[1], m[2])
		if secs >= blockSleepAtSeconds {
			return Decision{
				Action: Block,
				Reason: fmt.Sprintf(
					"Blocking wait of %gs (>= %ds) is not allowed in a dispatch turn. "+
						"A foreground sleep ties up the session doing nothing. "+
						"Instead: run the work with run_in_background and poll its status, "+
						"or poll the condition in a bounded loop across turns. "+
						"For sub-second pacing, keep it under 2s.",
					secs, blockSleepAtSeconds),
			}, true
		}
	}
	return Decision{}, false
}

// durationSeconds converts a sleep value + unit suffix into seconds. A missing
// or unrecognised suffix is treated as seconds (the sleep(1) default).
func durationSeconds(value, unit string) float64 {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	switch unit {
	case "m":
		return n * 60
	case "h":
		return n * 3600
	case "d":
		return n * 86400
	default:
		return n
	}
}
