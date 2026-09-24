package postedit

import (
	"encoding/json"
	"strings"
)

// discardBashPolicy names this wall's refusals in gate.log.
const discardBashPolicy = "discard-bash"

// discardBashRefusal is the operator's blanket directive: these git verbs
// are forbidden outright in a Bash/PowerShell call, no override — unlike
// WallDiscard's one-shot arm on the git shim's own cost-measured wall
// (git_shim_discard.go), which asks what an invocation would destroy before
// refusing it, this one asks nothing: the directive is "never run these".
// Kept byte-identical to the ad hoc hook it replaces so nothing downstream
// has to re-learn the line (user directive 2026-08-27).
const discardBashRefusal = "git checkout/restore/clean/reset --hard/stash drop|clear are forbidden: they discard uncommitted work (user directive 2026-08-27)"

// probeDiscardRoute is the sanctioned way back to HEAD for a refused probe
// arm, appended to the refusals an agent meets on the way to stripping one.
const probeDiscardRoute = "; to strip a refused probe arm back to HEAD, run `aphrollo gate probe discard <files>` " +
	"(dry run; --apply backs the diff up, then restores exactly those files)"

// reverseApplyRefusal refuses a reverse apply: `git diff > p && git apply -R
// p` restores the working tree exactly as `git checkout --` does, with no
// record of what it threw away (#836).
const reverseApplyRefusal = "git apply -R / patch -R discard uncommitted work the gate cannot audit (user directive 2026-08-27)" +
	probeDiscardRoute

// DiscardBashDecision judges a Bash/PowerShell PreToolUse payload against the
// discard-verb directive. It reads real command WORDS only, via the same
// quote-aware split bashWriteTargets already uses (shellSegments /
// shellWordTokens): text sitting inside a quoted argument — `gh pr create
// --body "...git checkout -- f..."` — is one opaque word to a real shell,
// never the separate words "git" and "checkout" a substring scan over the
// raw command text would see there (the false positive this replaces —
// same class of bug as #725's redirect scan). Two constructs run their
// content for real even though it is not one of the command's own
// top-level segments, so both are scanned recursively: `$(...)` command
// substitution, and a `bash -c "<script>"` / `sh -c "<script>"` /
// `zsh -c "<script>"` invocation.
func DiscardBashDecision(raw []byte) Decision {
	var in bashSuiteInput
	if err := json.Unmarshal(raw, &in); err != nil || !bashLikeTools[in.ToolName] {
		return Decision{}
	}
	cmd := strings.TrimSpace(in.ToolInput.Command)
	if cmd == "" {
		return Decision{}
	}
	reason, ok := cmdDiscards(cmd)
	if !ok {
		return Decision{}
	}
	return Decision{Action: Block, Reason: reason, Policy: discardBashPolicy}
}

// cmdDiscards returns the refusal for the first forbidden invocation cmd, or
// a script it really runs via command substitution or a `<shell> -c`,
// contains; ok=false when it contains none.
func cmdDiscards(cmd string) (reason string, ok bool) {
	stripped := stripHeredocBodies(cmd)
	for _, words := range shellSegments(stripped) {
		if reason, ok := segmentDiscards(words); ok {
			return reason, true
		}
		if script, isScript := bashDashCScript(words); isScript {
			if reason, ok := cmdDiscards(script); ok {
				return reason, true
			}
		}
	}
	for _, body := range commandSubstitutionBodies(stripped) {
		if reason, ok := cmdDiscards(body); ok {
			return reason, true
		}
	}
	return "", false
}

// discardBashRule is one row of the wall: the program a segment runs, the
// git subcommand when that program is git (empty for any other program),
// the argument shape that makes the invocation a discard, and the refusal
// it earns. A new forbidden spelling is a new row, not a new branch.
type discardBashRule struct {
	program string
	verb    string
	match   func(rest []string) bool
	reason  string
}

// discardBashRules mirrors the shape of the raw regex the wall replaced
// (`git\s+(checkout|clean|reset\s+--(hard|merge)|stash\s+(drop|clear)|
// restore(?!\s+--staged))`).
var discardBashRules = []discardBashRule{
	{program: "git", verb: "checkout", match: anyArgs, reason: discardBashRefusal + probeDiscardRoute},
	{program: "git", verb: "clean", match: anyArgs, reason: discardBashRefusal},
	{program: "git", verb: "reset", match: resetDiscards, reason: discardBashRefusal},
	{program: "git", verb: "stash", match: stashDiscards, reason: discardBashRefusal},
	{program: "git", verb: "restore", match: restoreDiscards, reason: discardBashRefusal + probeDiscardRoute},
	{program: "git", verb: "apply", match: reverses, reason: reverseApplyRefusal},
	{program: "patch", match: reverses, reason: reverseApplyRefusal},
}

func anyArgs([]string) bool { return true }

func resetDiscards(rest []string) bool {
	return containsToken(rest, "--hard") || containsToken(rest, "--merge")
}

func stashDiscards(rest []string) bool {
	return len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear")
}

// reverses reports whether args ask git apply or patch(1) to apply in
// reverse: `--reverse`, `-R`, or a short-option cluster led by R (`-Rp1`,
// `-Rv`). A cluster with R further in is left alone: patch's `-d DIR` and
// `-D NAME` take their value in the same token, and `-dREPO` is no reverse.
func reverses(args []string) bool {
	for _, a := range args {
		if a == "--reverse" || strings.HasPrefix(a, "-R") {
			return true
		}
	}
	return false
}

func restoreDiscards(rest []string) bool {
	return len(rest) == 0 || rest[0] != "--staged"
}

// segmentDiscards judges one already-split command (a shellSegments entry)
// against discardBashRules. A git invocation is judged by its VERB, with
// git's own global options (-C, -c, --git-dir, ...) read out of the way
// first, so `git -C dir clean -fd` is judged by "clean", not by whatever
// token happens to sit right after "git".
func segmentDiscards(words []string) (reason string, ok bool) {
	if len(words) == 0 {
		return "", false
	}
	program, verb, rest := baseCommand(words[0]), "", words[1:]
	if v, r, isGit := gitVerb(words); isGit {
		verb, rest = v, r
	}
	for _, rule := range discardBashRules {
		if rule.program == program && rule.verb == verb && rule.match(rest) {
			return rule.reason, true
		}
	}
	return "", false
}

// gitGlobalOptWithValue are git's own global options that consume a separate
// following argument. Kept as its own small copy rather than importing
// internal/cli's gitGlobalArgs: internal/cli already imports this package,
// so the reverse import would cycle.
var gitGlobalOptWithValue = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
}

// gitVerb returns the git subcommand a segment invokes, once its own leading
// global options are skipped, or ok=false when words is not a git
// invocation at all.
func gitVerb(words []string) (verb string, rest []string, ok bool) {
	if len(words) == 0 || baseCommand(words[0]) != "git" {
		return "", nil, false
	}
	// A global option's separate value is consumed by setting skip rather
	// than by stepping the index inside the loop: an in-loop step is a
	// mutation site whose decrement never terminates, which a mutation run
	// can only report as a timeout and never as a caught mutant.
	skip := false
	for i, w := range words {
		if i == 0 {
			continue
		}
		if skip {
			skip = false
			continue
		}
		if !strings.HasPrefix(w, "-") {
			return w, words[i+1:], true
		}
		if gitGlobalOptWithValue[w] {
			skip = true
		}
	}
	return "", nil, false
}

// containsToken reports whether any word in words equals tok exactly.
func containsToken(words []string, tok string) bool {
	for _, w := range words {
		if w == tok {
			return true
		}
	}
	return false
}

// bashDashCScript returns the script a `bash -c`/`sh -c`/`zsh -c` segment
// would run. shellSegments has already stripped its quoting, so words'
// entries are exactly the text the shell itself would execute, not a
// re-quoted guess at it.
func bashDashCScript(words []string) (script string, ok bool) {
	if len(words) == 0 {
		return "", false
	}
	switch baseCommand(words[0]) {
	case "bash", "sh", "zsh":
	default:
		return "", false
	}
	for i := 1; i+1 < len(words); i++ {
		if words[i] == "-c" {
			return words[i+1], true
		}
	}
	return "", false
}

// commandSubstitutionBodies extracts the text of every unquoted `$(...)` in
// cmd — found over mask(cmd), so a `$(` sitting inside a quoted argument
// (data, never executed) is not mistaken for one that runs. Nested parens
// inside the body are balanced, so `$(echo $(git checkout -- f))` still
// finds the whole outer body rather than stopping at its first `)`.
func commandSubstitutionBodies(cmd string) []string {
	masked := mask(cmd)
	var bodies []string
	for i := 0; i+1 < len(masked); i++ {
		if masked[i] != '$' || masked[i+1] != '(' {
			continue
		}
		depth := 1
		j := i + 2
		for ; j < len(masked) && depth > 0; j++ {
			switch masked[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth == 0 {
			bodies = append(bodies, cmd[i+2:j-1])
			i = j - 1
		}
	}
	return bodies
}
