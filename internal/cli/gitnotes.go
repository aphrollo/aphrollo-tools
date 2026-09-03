package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The gate note is the whole channel to CI, and a note nobody pushed reaches
// no runner. Notes do NOT ride along with a branch push — `git push` sends
// refs/heads and refs/tags, never refs/notes — so somebody would have to
// remember `git push origin refs/notes/gate` after every push, which is a
// thing nobody remembers twice.
//
// The shim already brokers every index-mutating git invocation, so it is the
// one place that sees a push happen and can send the note with it. Strictly
// best effort: a push is the operator's command, and a notes ref that cannot
// be pushed (no permission on a protected remote, a note another box moved
// first) must never turn their successful push into a failure. One line on
// stderr, and the exit code is the push's own.

// pushGateNotes sends refs/notes/gate to the same remote a successful branch
// push just went to. It is a no-op for anything that is not a plain,
// successful push, and for a repo that has no note to send.
func pushGateNotes(rest []string, cwd, realGit string, code int, stderr io.Writer) {
	if code != 0 || len(rest) == 0 || rest[0] != "push" {
		return
	}
	remote := pushRemote(rest[1:])
	if remote == "" {
		return
	}
	if !hasLocalGateNotes(cwd, realGit) {
		return
	}
	cmd := exec.Command(realGit, "push", remote, tdd.GateNotesRefFull)
	cmd.Dir = cwd
	// Marked as already-queued: this process holds the per-repo lock, and a
	// child routed back through the shim by PATH would wait on it forever.
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "gate: pushed the branch but not %s (%v: %s) — CI will read this tip as ungated\n",
			tdd.GateNotesRefFull, err, strings.Join(strings.Fields(string(out)), " "))
	}
}

// pushRemote is the remote a push names, "" when the arguments are a shape
// this should stay out of: an explicit refspec (the operator is pushing
// something specific), a delete, or a mirror.
func pushRemote(args []string) string {
	remote, positionals := "", 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--delete" || a == "-d" || a == "--mirror" || a == "--all":
			return ""
		case strings.HasPrefix(a, "-"):
			continue
		case strings.Contains(a, ":") && positionals > 0:
			// An explicit src:dst refspec. Leave it alone — and never
			// recurse on a push of the notes ref itself.
			return ""
		default:
			positionals++
			if positionals == 1 {
				remote = a
			}
		}
	}
	if remote == "" {
		// A bare `git push` goes to the branch's upstream remote; origin is
		// the only name worth guessing, and a wrong guess costs one failed
		// git process and one stderr line.
		remote = "origin"
	}
	if remote == tdd.GateNotesRefFull {
		return ""
	}
	return remote
}

// hasLocalGateNotes reports whether there is a note to send at all, so a repo
// the gate has never stamped does not spend a git process on every push.
func hasLocalGateNotes(cwd, realGit string) bool {
	cmd := exec.Command(realGit, "rev-parse", "--verify", "--quiet", tdd.GateNotesRefFull)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	return cmd.Run() == nil
}
