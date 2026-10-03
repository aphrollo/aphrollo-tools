package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/aphrollo/aphrollo-tools/internal/run"
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
	push := func() ([]byte, error) { return lightCombined(gateNotesPushCmd(realGit, cwd, remote)) }
	out, err := pushNotesMerging(
		push,
		push,
		func() error { return mergeRemoteGateNotes(realGit, cwd, remote) })
	if err != nil {
		fmt.Fprintf(stderr, "gate: pushed the branch but not %s (%v: %s) — CI will read this tip as ungated\n",
			tdd.GateNotesRefFull, err, strings.Join(strings.Fields(string(out)), " "))
	}
}

// notesPushRejected reports whether a notes push failed because the remote's
// ref holds notes the local one lacks ("fetch first", or a plain
// non-fast-forward), which a merge of the two settles.
func notesPushRejected(output string) bool {
	return strings.Contains(output, "fetch first") || strings.Contains(output, "non-fast-forward")
}

// notesMergeAttempts bounds how many times a rejected notes push is followed
// by a fetch, a merge and another push: a box pushing at the same moment can
// move the remote's ref again between two of them.
const notesMergeAttempts = 3

// pushNotesMerging runs the first push, and while the remote rejects it as
// behind, up to notesMergeAttempts times, merges the remote's notes in and
// pushes again. Any other failure, a failed merge, or the last rejection is
// the result.
func pushNotesMerging(first func() ([]byte, error), again func() ([]byte, error), merge func() error) ([]byte, error) {
	out, err := first()
	for range notesMergeAttempts {
		if err == nil || !notesPushRejected(string(out)) || merge() != nil {
			break
		}
		out, err = again()
	}
	return out, err
}

// scratchNotesCounter makes two scratch refs of one process differ.
var scratchNotesCounter atomic.Uint64

// scratchNotesRef is where the remote's notes are fetched to before they are
// merged (a notes merge reads its other side from a ref under refs/notes/),
// private to this process and call so two pushes at once never share it.
func scratchNotesRef() string {
	return fmt.Sprintf("refs/notes/gate-remote-scratch-%d-%d", os.Getpid(), scratchNotesCounter.Add(1))
}

// mergeRemoteGateNotes brings the remote's refs/notes/gate into the local one
// so a push of it fast-forwards. cat_sort_uniq keeps every line of both
// sides of a note both boxes wrote, so no note is lost to either. The scratch
// ref is removed whatever happens. Any failure leaves the local notes as they
// were, and the caller reports the rejection.
func mergeRemoteGateNotes(realGit, dir, remote string) error {
	git := func(args ...string) error {
		out, err := lightCombined(run.Spec{Name: realGit, Args: append([]string{"-c", "credential.interactive=false", "-c", "core.askPass="}, args...), Dir: dir,
			Env: append(os.Environ(), tdd.GitQueuedEnv+"=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")})
		if err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, strings.Join(strings.Fields(string(out)), " "))
		}
		return nil
	}
	scratch := scratchNotesRef()
	defer func() { _ = git("update-ref", "-d", scratch) }()
	if err := git("fetch", "--no-tags", remote, "+"+tdd.GateNotesRefFull+":"+scratch); err != nil {
		return err
	}
	return git("notes", "--ref="+tdd.GateNotesRefFull, "merge", "-s", "cat_sort_uniq", scratch)
}

// pushValueFlags are `git push` flags whose value is a SEPARATE argv token
// rather than folded into the flag itself, and whose value is never a
// remote name (an opaque server-side string, a program path, or a
// recursion mode) -- so it is always safe to discard, never to capture as
// the resolved remote. `--flag=value` and an attached short form already
// carry their own value in one token and are matched by the general
// "-"-prefix case instead; this map only catches the two-token form.
//
// `--repo <repository>` is deliberately ABSENT: unlike the flags below, its
// value literally IS the intended remote (git: "--repo is equivalent to the
// <repository> argument"), so the two-token form already resolves correctly
// by falling through to the ordinary positional case below -- special-casing
// it here would make pushRemote discard the one token that carries the
// answer. Its "=" form, `--repo=X`, does NOT fall through the same way: the
// general "-"-prefixed case below matches the whole self-contained token and
// discards it outright, so pushRemote carries a dedicated case for it,
// below, that extracts the value instead. The other flags in this map have
// no such gap -- their own "=" forms are already self-contained tokens whose
// VALUE is never the remote, so the general case correctly discards them
// too. `--force-with-lease[=...]` and `--signed[=...]` are also absent: both
// are OPTIONAL-argument flags in git's own grammar, so only the "=" form is
// legal and the space-separated form this map exists for cannot occur.
// `-u`/`--set-upstream` takes no argument at all.
var pushValueFlags = map[string]bool{
	"-o":                   true,
	"--push-option":        true,
	"--receive-pack":       true,
	"--exec":               true,
	"--recurse-submodules": true,
}

// gateNotesPushCmd builds the notes push. It can never ask anyone anything:
// this runs behind the operator's own push, unattended, and a git that
// decides it needs a credential opens `git-askpass` -- a WINDOW, which
// blocks the push behind it until a human dismisses it, once per push. The
// prompt is closed off at every door git has: the terminal prompt, both
// askpass hooks, and the credential helper's own interactive mode. A note
// that cannot be pushed without credentials is a note that does not get
// pushed, and says so in one line.
func gateNotesPushCmd(realGit, dir, remote string) run.Spec {
	return run.Spec{
		Name: realGit,
		Args: []string{
			"-c", "credential.interactive=false",
			"-c", "core.askPass=",
			"push", remote, tdd.GateNotesRefFull,
		},
		Dir: dir,
		// Marked as already-queued: this process holds the per-repo lock, and a
		// child routed back through the shim by PATH would wait on it forever.
		Env: append(os.Environ(),
			tdd.GitQueuedEnv+"=1",
			"GIT_TERMINAL_PROMPT=0",
			"GIT_ASKPASS=",
			"SSH_ASKPASS=",
		),
	}
}

// pushRemote is the remote a push names, "" when the arguments are a shape
// this should stay out of: an explicit refspec (the operator is pushing
// something specific), a delete, or a mirror.
func pushRemote(args []string) string {
	remote, positionals := "", 0
	sawSeparator := false
	// A flag's separate value is consumed by setting skip rather than by
	// stepping the index inside the loop: an in-loop step is a mutation site
	// whose decrement never terminates, which a mutation run can only report
	// as a timeout and never as a caught mutant.
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if !sawSeparator && a == "--" {
			// git's own separator: every token after it is positional, even
			// one that starts with "-", so flag matching stops here.
			sawSeparator = true
			continue
		}
		if sawSeparator {
			positionals++
			if positionals == 1 {
				remote = a
			}
			continue
		}
		switch {
		case a == "--delete" || a == "-d" || a == "--mirror" || a == "--all":
			return ""
		case pushValueFlags[a]:
			// Its value is the NEXT argv token, not a positional -- e.g.
			// `-o ci.skip origin main` must not read "ci.skip" as the remote.
			skip = true
			continue
		case strings.HasPrefix(a, "--repo="):
			// Self-contained like `--push-option=X`, but unlike those flags
			// its value IS the remote, so it is captured as the positional
			// fallthrough would capture the two-token form's value.
			positionals++
			if positionals == 1 {
				remote = strings.TrimPrefix(a, "--repo=")
			}
			continue
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
	return lightRun(run.Spec{Name: realGit, Args: []string{"rev-parse", "--verify", "--quiet", tdd.GateNotesRefFull}, Dir: cwd, Env: append(os.Environ(), tdd.GitQueuedEnv+"=1")}) == nil
}
