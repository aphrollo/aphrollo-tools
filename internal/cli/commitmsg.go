package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateCommitMsg is the `commit-msg` git hook: git passes the path to the
// message file, and a non-zero exit is the only thing that stops the commit.
// Everything else about this gate fails OPEN — it protects a convention, not
// correctness, so a hook called with no file (a broken install) lets the
// commit through rather than wedging every commit in the repo.
func runGateCommitMsg(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("commitmsg", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repository whose workspace manifest configures the check")
	// git passes the message path positionally, before any flag we add.
	var msg string
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		msg, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 0
	}
	if msg == "" {
		fmt.Fprintln(stderr, "aphrollo tdd commitmsg: no message file given — passing the commit through")
		return 0
	}
	root := tdd.RepoRoot(*repo)
	if root == "" {
		root = *repo
	}
	// Carry the gate's verdict into the commit, before the message is judged:
	// this is the only channel a CI runner has for "the local gate passed on
	// this exact tree", and it is written only when the pre-commit gate
	// stamped THIS tree green.
	tdd.AppendGateTrailer(root, msg)

	res := tdd.CommitMsg(root, msg)
	if !res.Blocked {
		return 0
	}
	fmt.Fprintln(stderr, res.Message)
	return 1
}
