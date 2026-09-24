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
	if cmd == "" || !cmdDiscards(cmd) {
		return Decision{}
	}
	return Decision{Action: Block, Reason: discardBashRefusal, Policy: discardBashPolicy}
}

// cmdDiscards reports whether cmd, or a script it really runs via command
// substitution or a `<shell> -c`, invokes one of the forbidden git verbs.
func cmdDiscards(cmd string) bool {
	stripped := stripHeredocBodies(cmd)
	for _, words := range shellSegments(stripped) {
		if segmentDiscards(words) {
			return true
		}
		if script, ok := bashDashCScript(words); ok && cmdDiscards(script) {
			return true
		}
	}
	for _, body := range commandSubstitutionBodies(stripped) {
		if cmdDiscards(body) {
			return true
		}
	}
	return false
}

// segmentDiscards judges one already-split command (a shellSegments entry)
// against the forbidden verbs, mirroring the shape of the raw regex this
// replaces (`git\s+(checkout|clean|reset\s+--(hard|merge)|stash\s+(drop|
// clear)|restore(?!\s+--staged))`) but reading git's own global options
// (-C, -c, --git-dir, ...) out of the way first, so `git -C dir clean -fd`
// is judged by its VERB, "clean", not by whatever token happens to sit
// right after "git".
func segmentDiscards(words []string) bool {
	verb, rest, ok := gitVerb(words)
	if !ok {
		return false
	}
	switch verb {
	case "checkout", "clean":
		return true
	case "reset":
		return containsToken(rest, "--hard") || containsToken(rest, "--merge")
	case "stash":
		return len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear")
	case "restore":
		return len(rest) == 0 || rest[0] != "--staged"
	}
	return false
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
	for i := 1; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") {
			return w, words[i+1:], true
		}
		if gitGlobalOptWithValue[w] {
			i++
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
