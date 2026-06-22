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

// pollFix is the shared fix hint for any foreground blocking wait — a long
// sleep or a watch/follow command. The sleep it recommends is sub-threshold
// (1s < blockSleepAtSeconds) so the suggestion does not itself trip the block.
const pollFix = "Instead: poll the condition in a bounded loop " +
	"(`until <condition>; do sleep 1; done`) or run the work with " +
	"run_in_background: true and poll its status."

// watchRes match foreground watch/follow commands that idle a dispatch turn the
// same way a long sleep does — they never return on their own. Matched
// case-insensitively against the masked command.
var watchRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bgh\b.*\s--watch\b`),               // gh ... --watch (e.g. gh pr checks --watch)
	regexp.MustCompile(`(?i)\btail\s+(-f|--follow)\b`),          // tail -f / tail --follow
	regexp.MustCompile(`(?i)\bjournalctl\b.*\s(-f|--follow)\b`), // journalctl -f / --follow
	regexp.MustCompile(`(?i)\bwatch\s+`),                        // the watch(1) command
}

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
	if d, hit := checkBlockingWatch(masked); hit {
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
						"A foreground sleep ties up the session doing nothing. %s "+
						"For sub-second pacing, keep it under %ds.",
					secs, blockSleepAtSeconds, pollFix, blockSleepAtSeconds),
			}, true
		}
	}
	return Decision{}, false
}

// checkBlockingWatch blocks foreground watch/follow commands (gh --watch, tail
// -f, journalctl -f, watch(1)). Like a long sleep they never return on their
// own and idle the whole dispatch turn; the fix is the same bounded poll loop.
func checkBlockingWatch(command string) (Decision, bool) {
	for _, re := range watchRes {
		if re.MatchString(command) {
			return Decision{
				Action: Block,
				Reason: "Blocking watch/follow command is not allowed in a dispatch turn. " +
					"It never returns on its own and ties up the session doing nothing. " +
					pollFix,
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
