package precommit

// goCIParityFlags are the flags CI's Go test job
// (.github/workflows/pipeline.yml, "Test (race + shuffle)") carries that
// DetectRunner's plain local default does not, and that cost nothing extra
// to run: -count=1 (defeats go's own test-result cache, which would
// otherwise let a cached pass from an EARLIER tree stand in for a run never
// made against the current one) and -shuffle=on (randomizes test order, so
// a test that quietly depends on another's leftover state — a lock file, a
// job record, a state dir — cannot pass here by declaration-order
// construction and then fail on whatever seed CI happens to pick). These
// two run at EVERY mechanical stage — precommit and premerge alike.
//
// CI's remaining flag, -timeout=180s, is deliberately NOT here. It was
// copied onto this box verbatim in an earlier version of this fix and
// measured live: of 29 mechanical runs after that landed, 28 panicked
// mid-test at 188-200s (never a genuine assertion failure) and one went
// green. -timeout=180s fits a clean single-job Linux CI runner; it does
// not fit internal/tdd's own suite on a Windows box running several
// concurrent lanes, and no local value copied from CI ever will, because
// the two machines' load characteristics are not the same problem. The one
// ceiling that belongs locally is the stage's own budget
// (DefaultPrecommitTimeout, enforced by RunSuite's context timeout) — a
// single number this repo already tunes for its own hardware, rather than
// a second, tighter one borrowed from a different machine entirely. See
// issue #421 (origin) and #434 (this correction).
var goCIParityFlags = []string{"-count=1", "-shuffle=on"}

// goRaceFlag is CI's remaining flag, kept separate from goCIParityFlags
// because it is not free: data race detection is several times slower to
// build and run, genuinely CPU/RAM-heavy, and this box runs several lanes
// concurrently. It is added ONLY at the merge stage (withGoCIParity's
// atMerge): the merge is where CI's verdict is about to be trusted and
// where this repo's own commit frequency is lowest, so it is the
// affordable place to pay for the one expensive flag — precommit, paid on
// every commit, stays on the cheap two. runCargoLocked additionally routes
// any go runner carrying this flag through the same build-slot governor a
// cargo build takes, so concurrent lanes' merges serialize instead of
// stacking.
const goRaceFlag = "-race"

// withGoCIParity inserts goCIParityFlags into a `go test` Runner, right
// after "test" — and goRaceFlag too when atMerge is true — so the
// mechanical stage answers the same question CI's authoritative run does,
// for the parts of that question this box can actually answer (see
// goCIParityFlags on -timeout). This deliberately does NOT run at post-edit
// at all: even the cheap flags stay off the fast advisory (issue #421's
// post-edit budget argument), which never calls this. A flag already
// present in runner.Args is left alone rather than duplicated, though no
// caller is expected to have named one of these already.
func withGoCIParity(r Runner, atMerge bool) Runner {
	if r.Cmd != "go" || len(r.Args) == 0 || r.Args[0] != "test" {
		return r
	}
	flags := goCIParityFlags
	if atMerge {
		flags = append([]string{goRaceFlag}, goCIParityFlags...)
	}
	present := make(map[string]bool, len(r.Args))
	for _, a := range r.Args[1:] {
		present[a] = true
	}
	args := make([]string, 0, len(r.Args)+len(flags))
	args = append(args, r.Args[0])
	for _, f := range flags {
		if !present[f] {
			args = append(args, f)
		}
	}
	args = append(args, r.Args[1:]...)
	return Runner{Cmd: r.Cmd, Args: args, Dir: r.Dir, Deadline: r.Deadline}
}
