package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The test gate already recognises a deliberate hand mutation proof: a bash
// command carrying the word "mutation"/"mutant" is allowed to re-run one test
// beside a fresh green, and every use is counted
// (hasMutationProofMarker, internal/tdd/bashsuite.go). The shim reads argv,
// not a command line, so the same marker reaches it as an ENVIRONMENT
// variable — `MUTATION=1 git checkout -- <file>` is one command that declares
// itself to both gates at once, since the word is in the command line the
// bash gate reads and in the environment this one reads.
//
// What the marker buys here is narrow on purpose: a path-scoped restore
// (`checkout -- <paths>`, `restore <paths>`) of files THIS SESSION HELD
// before mutating them. It is not a way past the discard wall for arbitrary
// `git checkout --` calls, and it does not reach `reset --hard`, `clean` or
// any other discarding verb.
const (
	mutationProofEnv = "MUTATION"
	mutantProofEnv   = "MUTANT"
)

// mutationProofDeclared reads the marker on the same loose terms the test
// gate's does: the session says which runs are proofs, and every use is
// counted. "0" and the empty string are the only values that are not a
// declaration, so `MUTATION=1`, `MUTATION=yes` and `MUTANT=1` all say it.
func mutationProofDeclared() bool {
	for _, name := range []string{mutationProofEnv, mutantProofEnv} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" && v != "0" {
			return true
		}
	}
	return false
}

// mutationProofRestore is the shim's half of the hand mutation proof's third
// step. handled=true means the shim answered this invocation itself and git
// must not run: either it restored the held bytes (code 0) or it refused
// (code 1).
//
// It restores from the session's HOLD rather than from the index, which is
// the whole of issue #650's third defect: `git checkout -- <file>` puts back
// what is staged, so mid-lane — where the mutated file also carries the
// lane's own uncommitted work — it deletes that work. There is no fallback to
// HEAD or to the index anywhere in here: with no hold to restore from, the
// shim refuses and says so, because a proof that corrupts the tree is worse
// than a proof that does not run.
//
// Untracked files are covered, and deliberately so: the hold is a byte copy,
// so a file git would refuse to check out at all ("pathspec did not match")
// restores exactly like a tracked one.
func mutationProofRestore(rest []string, workDir string, stderr io.Writer) (code int, handled bool) {
	if !mutationProofDeclared() {
		return 0, false
	}
	form, paths, ok := discardIntent(rest)
	if !ok || !isPathRestoreForm(form) || len(paths) == 0 {
		return 0, false
	}
	var held []tdd.MutationHold
	var unheld, stale []string
	for _, p := range paths {
		hold, live := tdd.MutationHoldFor(absUnder(workDir, p))
		switch {
		case live:
			held = append(held, hold)
		case hold.Path != "":
			stale = append(stale, fmt.Sprintf("%s (held %s ago)", p, hold.Age().Round(time.Second)))
		default:
			unheld = append(unheld, p)
		}
	}
	if len(unheld) > 0 || len(stale) > 0 {
		fmt.Fprintln(stderr, mutationRestoreRefusalLine(unheld, stale))
		tdd.AppendGateLog("git", workDir, "git", "git-discard-refused:mutation-restore-unheld", 0)
		return 1, true
	}
	for _, h := range held {
		if _, err := tdd.RestoreMutationHold(h.Path); err != nil {
			fmt.Fprintf(stderr, "gate: refused — the mutation-proof restore of %s failed: %v\n", h.Path, err)
			tdd.AppendGateLog("git", workDir, "git", "git-discard-refused:mutation-restore-failed", 0)
			return 1, true
		}
	}
	tdd.LogOverride("override-discard-mutation-proof", tdd.SessionID(), workDir)
	fmt.Fprintln(stderr, mutationRestoredLine(held))
	return 0, true
}

// isPathRestoreForm reports whether form is one of the two path-scoped
// restores a proof's third step actually uses. `checkout -f`, `reset --hard`
// and the rest stay the discard wall's business alone.
func isPathRestoreForm(form string) bool {
	return form == "checkout -- <paths>" || form == "restore <paths>"
}

// absUnder resolves a pathspec against the directory git was told to run in.
func absUnder(workDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

// mutationRestoreRefusalLine is the refusal for a declared proof the shim
// cannot serve: it names the files with no live hold, and both routes to one
// — take the hold before the mutation, or let `gate mutants prove` run the
// whole loop, which holds and restores for itself.
func mutationRestoreRefusalLine(unheld, stale []string) string {
	var parts []string
	if len(unheld) > 0 {
		parts = append(parts, fmt.Sprintf("this session holds no pre-mutation working state for %s", strings.Join(unheld, ", ")))
	}
	if len(stale) > 0 {
		parts = append(parts, fmt.Sprintf("the hold is stale for %s, past the %s a proof may hold a file", strings.Join(stale, ", "), tdd.MutationHoldTTL))
	}
	return fmt.Sprintf("gate: refused — a mutation-proof restore puts back the WORKING state the proof started from, and %s. "+
		"Falling back to `git checkout --` would restore from the index instead and take any unstaged work in the file with it, "+
		"which is what this path exists to stop. Take the hold BEFORE the mutation (`aphrollo gate mutants hold <file>`), or let "+
		"`aphrollo gate mutants prove --file --old --new --want-fail` run the whole loop, which holds and restores for itself",
		strings.Join(parts, "; "))
}

// mutationRestoredLine says what was put back, from what, and how old the
// held state is — the three things a reader needs to believe the tree is now
// what the proof started with.
func mutationRestoredLine(held []tdd.MutationHold) string {
	names := make([]string, 0, len(held))
	oldest := time.Duration(0)
	for _, h := range held {
		names = append(names, filepath.Base(h.Path))
		if age := h.Age(); age > oldest {
			oldest = age
		}
	}
	return fmt.Sprintf("gate: mutation proof — restored %d file(s) from the working state held %s ago, not from the index: %s",
		len(held), oldest.Round(time.Second), strings.Join(names, ", "))
}

// mutationHoldHint is what the ordinary discard refusal adds when the session
// is in the middle of a proof but did not say so on this command: the file it
// is refusing to overwrite is one this session holds a pre-mutation state
// for, and the restore it wants is one command away. Empty for every other
// refusal, so no other message changes shape.
func mutationHoldHint(form string, paths []string, workDir string) string {
	if !isPathRestoreForm(form) {
		return ""
	}
	var held []string
	for _, p := range paths {
		if _, live := tdd.MutationHoldFor(absUnder(workDir, p)); live {
			held = append(held, p)
		}
	}
	if len(held) == 0 {
		return ""
	}
	return fmt.Sprintf("; this session holds the pre-mutation working state of %s — %s=1 git %s restores THAT rather than the index",
		strings.Join(held, ", "), mutationProofEnv, restoreCommandFor(form, held))
}

// restoreCommandFor spells the command the hint recommends in the same form
// the caller used, so it can be run as printed.
func restoreCommandFor(form string, paths []string) string {
	if form == "restore <paths>" {
		return "restore -- " + strings.Join(paths, " ")
	}
	return "checkout -- " + strings.Join(paths, " ")
}
