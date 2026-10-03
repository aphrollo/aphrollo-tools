package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// trellisFile is the marker that a repo is gated by trellis. A repo that holds
// it is trellis's alone: aphrollo says nothing there and writes nothing, so the
// two never both gate one repo.
const trellisFile = "trellis.toml"

// holdsTrellis reports whether the repo dir sits in has trellis.toml at its
// root. Outside any repo there is nothing to hold it.
func holdsTrellis(dir string) bool {
	root := compat.RepoRoot(dir)
	if root == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, trellisFile))
	return err == nil
}

// silenceHooks are the gate subcommands an editor or git calls. Each one stands
// down in a trellis repo: exit 0, no output, no state.
var silenceHooks = append(slices.Clone(compatGitHooks), "sessionstart", "pretooluse", "posttooluse", "userpromptsubmit", "sessionend", "stop", "subagentstop", "taskcompleted")

// silenceGuard runs before the compat guard. A hook in a repo that holds
// trellis.toml is answered here with nothing, and a queue shim is told to pass
// straight through; every other command goes on with stdin as it was.
func silenceGuard(args []string, stdin io.Reader) (rest io.Reader, handled bool) {
	sub := compatGateSub(args)
	payloadHook := slices.Contains(compatClaudeHooks, sub) || sub == "stop" || sub == "subagentstop" || sub == "taskcompleted" ||
		slices.Equal(args[:min(len(args), 2)], []string{"guardrail", "pretooluse"})
	dir := compatRepoFlag(args)
	if payloadHook {
		raw, err := io.ReadAll(stdin)
		stdin = io.MultiReader(bytes.NewReader(raw), tailReader{err})
		dir = compatHookDir(raw)
	}
	switch {
	case !payloadHook && !slices.Contains(silenceHooks, sub) && sub != "git" && sub != "cargo":
		return stdin, false
	case !holdsTrellis(dir):
		return stdin, false
	case sub == "git":
		os.Setenv(tdd.GitQueuedEnv, "1")
		return stdin, false
	case sub == "cargo":
		os.Setenv(tdd.BuildLockHeldEnv, "1")
		return stdin, false
	}
	return stdin, true
}
