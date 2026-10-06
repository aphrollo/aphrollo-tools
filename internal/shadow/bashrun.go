package shadow

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/shell"
)

// BashRun is a test suite the agent started itself from Bash or PowerShell: the
// runner's words and the directory it started in. The parse reads the command's
// text alone and spawns nothing.
type BashRun struct {
	Argv []string
	Dir  string
}

// ParseBashRun reads the one test run a command line holds. It reports false for
// a line whose exit status is not one suite's: a pipeline (the status is the last
// command's), two runs, a run that only lists or builds, and a run in a directory
// the parse cannot name (a `cd` into a variable, `pushd`).
func ParseBashRun(cmd, cwd string) (BashRun, bool) {
	cmd = shell.StripHeredocBodies(cmd)
	if strings.TrimSpace(cmd) == "" || hasPipe(cmd) {
		return BashRun{}, false
	}
	cur := cwd
	var run BashRun
	n := 0
	for _, seg := range shell.ShellSegments(cmd) {
		words := dropEnvWords(seg)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "cd":
			// A directory that resolves to "" is unknown, and stays unknown.
			next := ""
			if len(words) == 2 && words[1] != "-" {
				next = shell.ResolveAgainst(cur, words[1])
			}
			cur = next
			continue
		case "pushd", "popd":
			cur = ""
			continue
		}
		if argv, ok := testArgv(words); ok {
			n++
			run = BashRun{Argv: argv, Dir: cur}
		}
	}
	return run, n == 1 && run.Dir != ""
}

// dropEnvWords is words without the leading NAME=value words of a command.
func dropEnvWords(words []string) []string {
	for i, w := range words {
		if !isEnvWord(w) {
			return words[i:]
		}
	}
	return nil
}

func isEnvWord(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range w[:eq] {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// hasPipe reports whether the line holds a pipeline operator outside quotes.
func hasPipe(cmd string) bool {
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '\\':
			i++
		case '\'', '"':
			for i++; i < len(rs) && rs[i] != c; i++ {
				if c == '"' && rs[i] == '\\' {
					i++
				}
			}
		case '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				i++
				continue
			}
			return true
		}
	}
	return false
}

// testArgv is the runner's words when words are a command that runs tests, with a
// wrapper (time, uv run, poetry run) dropped, and false for one that does not run
// them (a build, a listing, a collection, any other command).
func testArgv(words []string) ([]string, bool) {
	w := words
	if len(w) > 0 && baseName(w[0]) == "time" {
		w = w[1:]
	}
	if len(w) > 2 && slices.Contains([]string{"uv", "poetry", "pipenv"}, baseName(w[0])) && w[1] == "run" {
		w = w[2:]
	}
	if len(w) == 0 {
		return nil, false
	}
	return runnerArgv(w)
}

func runnerArgv(w []string) ([]string, bool) {
	rest := w[1:]
	switch baseName(w[0]) {
	case "go":
		return w, len(rest) > 0 && rest[0] == "test" && !hasAny(rest[1:], "-c", "-list", "-h", "-help")
	case "cargo":
		test := len(rest) > 0 && rest[0] == "test" || len(rest) > 1 && rest[0] == "nextest" && rest[1] == "run"
		return w, test && !hasAny(rest, "--no-run", "--list")
	case "pytest", "py.test":
		return w, !hasAny(rest, "--collect-only", "--co", "-h", "--help", "--version")
	case "python", "python3", "py":
		return w, len(rest) > 1 && rest[0] == "-m" && rest[1] == "pytest" && !hasAny(rest, "--collect-only", "--co", "-h", "--help", "--version")
	case "vitest", "jest":
		return w, !hasAny(rest, "-h", "--help", "--version", "--listTests")
	case "npx", "bunx":
		return w, len(rest) > 0 && (rest[0] == "vitest" || rest[0] == "jest") && !hasAny(rest, "-h", "--help", "--version", "--listTests")
	case "npm", "pnpm", "yarn", "bun":
		return w, len(rest) > 0 && (rest[0] == "test" || rest[0] == "t" || len(rest) > 1 && rest[0] == "run" && rest[1] == "test")
	}
	return nil, false
}

// hasAny reports whether args holds one of names, as a word or as name=value.
func hasAny(args []string, names ...string) bool {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(names, name) {
			return true
		}
	}
	return false
}

// baseName is a command word's name without its directory or extension.
func baseName(w string) string {
	b := filepath.Base(filepath.FromSlash(w))
	return strings.ToLower(strings.TrimSuffix(b, filepath.Ext(b)))
}

// BashResult is what a finished shell call reports of itself: its exit status, what
// it printed and how long it took.
type BashResult struct {
	Exit       int
	Output     string
	ToolUseID  string
	DurationMS int64
}

var exitCodeRe = regexp.MustCompile(`^Exit code (\d+)\n?`)

// BashResult reads the result of a Bash or PowerShell call from its PostToolUse
// payload (a call that exited 0: the response's stdout and stderr) or its
// PostToolUseFailure payload (a call that did not: `error` is "Exit code N" then
// the output). It reports false for anything else: an interrupted call, a call that
// failed to start, a payload with no result.
func (p Payload) BashResult() (BashResult, bool) {
	if !p.IsShell() {
		return BashResult{}, false
	}
	r := BashResult{ToolUseID: p.ToolUseID, DurationMS: p.DurationMS}
	if p.Error != "" {
		m := exitCodeRe.FindStringSubmatch(p.Error)
		if p.IsInterrupt || m == nil {
			return BashResult{}, false
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n == 0 {
			return BashResult{}, false
		}
		r.Exit, r.Output = n, strings.TrimPrefix(p.Error, m[0])
		return r, true
	}
	if len(p.ToolResponse) == 0 {
		return BashResult{}, false
	}
	var resp struct {
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
		Interrupted bool   `json:"interrupted"`
	}
	var text string
	switch {
	case json.Unmarshal(p.ToolResponse, &text) == nil:
		r.Output = text
	case json.Unmarshal(p.ToolResponse, &resp) == nil && !resp.Interrupted:
		r.Output = resp.Stdout
		if resp.Stderr != "" {
			if r.Output != "" && !strings.HasSuffix(r.Output, "\n") {
				r.Output += "\n"
			}
			r.Output += resp.Stderr
		}
	default:
		return BashResult{}, false
	}
	return r, true
}

// RunUnitName is how a run is named in a verdict file and in the fold of a run into
// the lane: the project it ran in, relative to the repository root ("." for the
// root itself), then the command's words, so two runs of one project over different
// packages are two runs.
func RunUnitName(root string, argv []string) string {
	return relOrDot(findUp(root, ".git"), root) + "|" + strings.Join(argv, " ")
}
