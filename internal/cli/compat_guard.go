package cli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// compatClass is what a command does when the repo it acts on declares a
// `requires` newer than this binary (internal/compat).
type compatClass int

const (
	// compatOpen runs on any binary: it reads no format a newer binary may
	// have changed, or it is the cure the refusal names.
	compatOpen compatClass = iota
	// compatHook is a hook the editor or git calls. It fails OPEN: the gate
	// does not judge what it cannot read, says so on one line, and lets the
	// edit or the commit through.
	compatHook
	// compatRefuse writes repo state, or judges the tree by laws, baselines
	// and sidecars a newer binary may have changed. It refuses with exit 1.
	compatRefuse
)

// The classification is an allow-list on purpose: a verb added later is
// refused under an unmet `requires` until somebody decides it reads no
// format, the safe direction for a binary that cannot know what the repo
// holds. The names mirror cli.go's and cli_gate.go's dispatch.
var (
	compatOpenTop = map[string]bool{
		"-h": true, "--help": true, "help": true,
		"version": true, "update": true, "status": true, "dev": true,
		"guardrail": true, "refactor": true, "find": true, "outline": true,
		"show": true, "config": true, "issue": true, "feedback": true, "ci": true,
	}
	// compatOpenGate holds the gate's session switches, reports and shims. The
	// cargo, git and lint shims wrap every build and git call on the box and must
	// never be the thing that stops one.
	compatOpenGate = map[string]bool{
		"allow": true, "revoke": true, "primary-edits": true, "doctor": true,
		"statusline": true, "stats": true, "output": true, "status": true,
		"classify-diff": true, "runphase": true, "selfcheck": true,
		"cargo": true, "git": true, "lint": true, "gc": true,
		"issue": true, "feedback": true,
	}
	compatGitHooks    = []string{"precommit", "premerge", "premergecommit", "prepush", "commitmsg", "postcommit", "postmerge"}
	compatClaudeHooks = []string{"sessionstart", "pretooluse", "posttooluse", "userpromptsubmit", "sessionend"}
)

// compatGateSub is the gate subcommand a command line names, "" for any other
// command. `tdd` is the pre-rename spelling of gate.
func compatGateSub(args []string) string {
	if len(args) > 1 && (args[0] == "gate" || args[0] == "tdd") {
		return args[1]
	}
	return ""
}

// compatClassOf sorts a command line into what it does under an unmet
// `requires`. Asking for help runs nothing, so a help flag in the first two
// arguments after the command (`ratchet -h`, `ratchet check --help`,
// `workspace help`) is never refused.
func compatClassOf(args []string) compatClass {
	if len(args) == 0 {
		return compatOpen
	}
	for _, a := range args[1:min(len(args), 3)] {
		if a == "-h" || a == "--help" || a == "help" {
			return compatOpen
		}
	}
	switch args[0] {
	case "gate", "tdd":
		sub := compatGateSub(args)
		switch {
		case sub == "" || compatOpenGate[sub]:
			return compatOpen
		case slices.Contains(compatGitHooks, sub) || slices.Contains(compatClaudeHooks, sub):
			return compatHook
		}
		return compatRefuse
	case "workspace":
		if len(args) == 1 || args[1] == "list" {
			return compatOpen
		}
		return compatRefuse
	}
	if compatOpenTop[args[0]] {
		return compatOpen
	}
	return compatRefuse
}

// compatRepoFlag is the repo a command was pointed at with --repo, in any of
// the spellings the flag package takes, the last one winning as it does; the
// working directory otherwise.
func compatRepoFlag(args []string) string {
	repo, wantValue := ".", false
	for _, a := range args {
		if wantValue {
			repo, wantValue = a, false
			continue
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, value, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "repo" {
			continue
		}
		if joined {
			repo = value
		} else {
			wantValue = true
		}
	}
	return repo
}

// compatHookDir is the directory an editor hook payload is about: the edited
// file's own, so a session in one checkout editing another is judged by the
// repo it edits, else the session's working directory.
func compatHookDir(raw []byte) string {
	var in struct {
		Cwd       string `json:"cwd"`
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return "."
	}
	switch file := in.ToolInput.FilePath; {
	case file == "" && in.Cwd == "":
		return "."
	case file == "":
		return in.Cwd
	case filepath.IsAbs(file):
		return filepath.Dir(file)
	default:
		return filepath.Dir(filepath.Join(in.Cwd, file))
	}
}

// tailReader ends a stream with the error the guard's own read met, or with
// EOF when it met none, so a hook that reads what the guard already consumed
// sees exactly what it would have seen alone.
type tailReader struct{ err error }

// Read never returns (0, nil): with no error to report it says EOF, because a
// reader that answers nothing and no end would hold the hook that reads it
// until the editor kills it.
func (r tailReader) Read([]byte) (int, error) {
	return 0, cmp.Or(r.err, io.EOF)
}

// compatGuard runs before every command. It answers a command whose repo
// declares a newer minimum than this binary and returns handled; any other
// command goes on to its own dispatch with stdin as it was.
func compatGuard(args []string, stdin io.Reader, stdout, stderr io.Writer) (rest io.Reader, code int, handled bool) {
	class := compatClassOf(args)
	if class == compatOpen {
		return stdin, 0, false
	}
	dir := compatRepoFlag(args)
	if slices.Contains(compatClaudeHooks, compatGateSub(args)) {
		raw, err := io.ReadAll(stdin)
		stdin = io.MultiReader(bytes.NewReader(raw), tailReader{err})
		dir = compatHookDir(raw)
	}
	verdict := compat.CheckAt(dir, compat.Binary())
	if verdict.Status == compat.Satisfied {
		return stdin, 0, false
	}
	if class == compatRefuse {
		fmt.Fprintln(stderr, verdict.Line)
		return stdin, 1, true
	}
	if compatGateSub(args) == "sessionstart" {
		// The one hook whose output reaches the model; the others' stderr is
		// shown to nobody while they exit 0.
		payload, _ := tdd.RenderSessionStart(verdict.Line)
		stdout.Write(payload)
		return stdin, 0, true
	}
	fmt.Fprintln(stderr, verdict.Line)
	return stdin, 0, true
}
