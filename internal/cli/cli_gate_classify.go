package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const classifyDiffUsage = `usage: aphrollo gate classify-diff [--json] <base> [<head>]

Print the class of the change from <base> to <head> (default HEAD, and it
must be the checked-out commit): docs-only, comment-only, workflow-only or
code. Read-only. The class comes from the commit gate's own per-file rules
(file kind with //go:embed awareness, token-level comment-only comparison
for Go and Rust). Any failure (a base the clone does not hold, no
repository, a git error) prints code with the reason on stderr and still
exits 0, so a caller reading stdout can never land on a fast path it did
not earn. --json prints {"class": ..., "reason": ...} instead.
`

// runGateClassifyDiff is CI's `changes` job's classifier (pipeline.yml):
// one class on stdout, exit 0 whatever the class; exit 2 only on a bad
// invocation.
func runGateClassifyDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate classify-diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, classifyDiffUsage) }
	asJSON := fs.Bool("json", false, "print {\"class\", \"reason\"} as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 {
		fmt.Fprint(stderr, classifyDiffUsage)
		return 2
	}
	head := "HEAD"
	if len(rest) == 2 {
		head = rest[1]
	}

	class, err := classifyFromCheckout(rest[0], head)
	reason := ""
	if err != nil {
		reason = err.Error()
		fmt.Fprintf(stderr, "gate classify-diff: %s; answering %s\n", reason, class)
	}
	if *asJSON {
		b, _ := json.Marshal(struct {
			Class  tdd.DiffClass `json:"class"`
			Reason string        `json:"reason"`
		}{class, reason})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintln(stdout, class)
	return 0
}

// classifyFromCheckout finds the repository the working directory sits in
// and classifies from its root, which is where ClassifyFile's embed rule
// resolves its relative paths.
func classifyFromCheckout(base, head string) (tdd.DiffClass, error) {
	var errb bytes.Buffer
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Stderr = &errb // quoted in the reason below
	out, err := cmd.Output()
	if err != nil {
		return tdd.DiffCode, fmt.Errorf("not inside a git repository: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	root := strings.TrimSpace(string(out))
	if err := os.Chdir(root); err != nil {
		return tdd.DiffCode, fmt.Errorf("cannot enter the repository root %s: %v", root, err)
	}
	return tdd.ClassifyDiff(root, base, head)
}
