package postedit

import (
	"encoding/json"
	"regexp"
	"strings"
)

// directPROpenPolicy names this wall's refusals in gate.log.
const directPROpenPolicy = "direct-pr-open"

// directPROpenRefusal names the path a PR opens by in a repo that measures
// mutants before it. `workspace pr`/`ship`/`submit` run gh as their own
// subprocess, never through this hook, so they are unaffected by it.
const directPROpenRefusal = "this repo declares mutants-before-pr = true: open the PR with `aphrollo workspace pr` " +
	"(or `workspace ship`/`submit`), which measures the lane's mutants first; " +
	"a direct `gh pr create` or `gh api` POST to repos/<owner>/<repo>/pulls skips that measurement (#871)"

// DirectPROpenDecision refuses a Bash/PowerShell command that opens a pull
// request directly when the repo declares mutants-before-pr: such a PR never
// passes through the verbs that measure the lane first (issue #871, where a
// raw `gh pr create` let five survivors reach CI). The command is read as
// real words, the same way the discard wall reads it, so the words inside a
// quoted argument or on a comment line are data. The repo is the one the shell's cwd
// sits in; with the key off or absent the wall is inert.
func DirectPROpenDecision(raw []byte) Decision {
	var in bashSuiteInput
	if err := json.Unmarshal(raw, &in); err != nil || !bashLikeTools[in.ToolName] {
		return Decision{}
	}
	if !scanCommand(in.ToolInput.Command, segmentOpensPR) || !mutantsBeforePRDeclared(in.Cwd) {
		return Decision{}
	}
	return Decision{Action: Block, Reason: directPROpenRefusal, Policy: directPROpenPolicy}
}

// mutantsBeforePRDeclared reports whether the git repo holding cwd declares
// mutants-before-pr = true. A config that cannot be read declares nothing.
func mutantsBeforePRDeclared(cwd string) bool {
	root := RepoRoot(cwd)
	if root == "" {
		return false
	}
	cfg, err := ReadMutantsConfig(root)
	return err == nil && cfg.BeforePR
}

// prCreateVerbs are the spellings of `gh pr create`: `new` is its alias.
var prCreateVerbs = map[string]bool{"create": true, "new": true}

// segmentOpensPR reports whether one simple command opens a pull request:
// `gh pr create` with its options anywhere, or a `gh api` POST to a
// repository's pulls endpoint.
func segmentOpensPR(words []string) bool {
	if len(words) == 0 || baseCommand(words[0]) != "gh" {
		return false
	}
	cmd := ghCommandWords(words)
	switch wordAt(cmd, 0) {
	case "pr":
		return prCreateVerbs[wordAt(cmd, 1)]
	case "api":
		return ghAPIOpensPR(words)
	}
	return false
}

// ghCommandWords returns the words of a gh invocation that are not options,
// with the value of gh's own -R/--repo option read out of the way, so
// `gh -R o/r pr create` is judged by "pr create".
func ghCommandWords(words []string) []string {
	var cmd []string
	skip := false
	for _, w := range words[1:] {
		switch {
		case skip:
			skip = false
		case w == "-R" || w == "--repo":
			skip = true
		case !strings.HasPrefix(w, "-"):
			cmd = append(cmd, w)
		}
	}
	return cmd
}

// wordAt returns words[i], or "" past the end.
func wordAt(words []string, i int) string {
	if i < len(words) {
		return words[i]
	}
	return ""
}

// pullsEndpoint is the REST endpoint a POST to which opens a pull request.
var pullsEndpoint = regexp.MustCompile(`^/?repos/[^/]+/[^/]+/pulls/?(\?.*)?$`)

// ghAPIMethodFlags and ghAPIFieldFlags are the `gh api` options that set the
// request method, and the ones that send a body: with a body and no method
// gh sends a POST.
var (
	ghAPIMethodFlags = map[string]bool{"-X": true, "--method": true}
	ghAPIFieldFlags  = map[string]bool{"-f": true, "-F": true, "--field": true, "--raw-field": true, "--input": true}
)

// ghAPIOpensPR reports whether a `gh api` invocation POSTs to a pulls
// endpoint, the method either named or implied by a request body.
func ghAPIOpensPR(args []string) bool {
	method, body, pulls := "", false, false
	for i, a := range args {
		name, value, attached := splitOption(a)
		if !attached && i+1 < len(args) {
			value = args[i+1]
		}
		switch {
		case ghAPIMethodFlags[name]:
			method = value
		case ghAPIFieldFlags[name]:
			body = true
		case pullsEndpoint.MatchString(a):
			pulls = true
		}
	}
	return pulls && (strings.EqualFold(method, "POST") || method == "" && body)
}

// splitOption splits one word into an option name and the value attached to
// it: `--method=POST` and `-XPOST` both name their value in the same word.
// Any other word is its own name with nothing attached.
func splitOption(a string) (name, value string, attached bool) {
	if strings.HasPrefix(a, "--") {
		return strings.Cut(a, "=")
	}
	if strings.HasPrefix(a, "-") && len(a) > 2 {
		return a[:2], a[2:], true
	}
	return a, "", false
}
