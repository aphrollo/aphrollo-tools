package cli

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// cargo-mutants is the one cargo subcommand that can hold the box hostage.
// Run bare it copies the whole tree into the OS temp dir and builds it cold,
// once per job, for hours — measured: two runs, post-edit hooks waiting up to
// 619 s, 56 deferred in three hours, 11 tree copies left behind at ~135 MB
// each. Run through `aphrollo gate mutants run` it is inside the box-wide
// mutation lock, which is what keeps a second one from ever starting beside
// it.
//
// So the shim takes two positions on it, and they are the same position: the
// bare invocation is REFUSED, and the gated one is let PAST the queue —
// because the gated run already holds the lock the queue exists to arbitrate,
// and queueing it behind an editor's build only makes it hold that lock for
// longer.

// mutantsRefusal is the whole rejection: what to run instead, and why the
// invocation typed was not it.
const mutantsRefusal = "gate: run `aphrollo gate mutants run` — bare cargo mutants builds a cold copy in the OS temp dir and holds the build lock for hours, and a producer invoked directly runs outside the box-wide mutation lock"

// gateMarkedRun reports whether this invocation is a child of `aphrollo gate
// mutants run`, which puts tdd.MutationGateEnv in the environment it hands
// its children and is the only thing that does. One marker answers both
// questions the shim asks about a mutation run — may it invoke `mutants`, and
// may it skip the queue — because both are earned by the same fact: the call
// is already inside the box-wide mutation lock.
func gateMarkedRun() bool {
	return os.Getenv(tdd.MutationGateEnv) == tdd.MutationGateMarked
}

// refuseBareMutants reports whether this invocation is a hand-typed `cargo
// mutants`, and prints the one line that says what to do instead.
func refuseBareMutants(args []string, stderr io.Writer) bool {
	if cargoVerb(args) != "mutants" || gateMarkedRun() {
		return false
	}
	fmt.Fprintln(stderr, mutantsRefusal)
	return true
}

// queueBypassAllowed reports whether this invocation may skip the build queue
// entirely. The mutation run may, and nothing else does: it holds the
// box-wide mutation lock for its whole call, so there is at most one of it on
// the box, and making it wait for an editor's build only lengthens the window
// in which nobody else can start one. A caller that sets the marker without
// being that run bypasses too — the harm is bounded to its own target dir,
// and every use is counted (see logQueueBypass).
func queueBypassAllowed() bool { return gateMarkedRun() }

// bypassLogged makes the bypass record itself exactly once per process: a
// build invokes the shim many times, and one line per invocation would drown
// the log it is meant to make readable.
var bypassLogged sync.Once

// resetBypassLog lets a test observe the once-per-process rule.
func resetBypassLog() { bypassLogged = sync.Once{} }

// logQueueBypass records that a run went around the build queue. The bypass is
// a TOLERATED hole — any process can set the marker the gate's runner sets and
// the shim will honour it. The harm is bounded to that one target dir, and
// what makes it tolerable is that every use is counted: `gate stats` shows it
// beside every other waiver.
func logQueueBypass(targetDir string) {
	bypassLogged.Do(func() {
		tdd.AppendGateLog("precommit", targetDir, "cargo", "queue-bypass", 0)
	})
}
