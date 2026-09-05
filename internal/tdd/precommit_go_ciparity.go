package tdd

// goCIParityFlags are the flags CI's Go test job
// (.github/workflows/pipeline.yml, "Test (race + shuffle)") carries that
// DetectRunner's plain local default does not, in the order CI passes them:
// -race (data race detection — off entirely otherwise, and races also
// change timing enough to turn a marginal assumption into a hang only CI
// sees), -count=1 (defeats go's own test-result cache, which would
// otherwise let a cached pass from an EARLIER tree stand in for a run never
// made against the current one), -shuffle=on (randomizes test order, so a
// test that quietly depends on another's leftover state — a lock file, a
// job record, a state dir — cannot pass here by declaration-order
// construction and then fail on whatever seed CI happens to pick), and
// -timeout=180s (CI's tighter bound; a local pass under a looser default
// says nothing about whether CI's would have timed out). See issue #421.
var goCIParityFlags = []string{"-race", "-count=1", "-shuffle=on", "-timeout=180s"}

// withGoCIParity inserts goCIParityFlags into a `go test` Runner, right
// after "test", so the mechanical stage — precommit's and premergecommit's
// shared gateRoot, the one place both already pay minutes to build and link
// the suite — answers the same question CI's authoritative run does. This
// deliberately does NOT run at post-edit: -race alone is several times
// slower and this box is already contended, so the fast advisory keeps the
// plain command (issue #421's post-edit budget argument). A flag already
// present in runner.Args is left alone rather than duplicated, though no
// caller is expected to have named one of these already.
func withGoCIParity(r Runner) Runner {
	if r.Cmd != "go" || len(r.Args) == 0 || r.Args[0] != "test" {
		return r
	}
	present := make(map[string]bool, len(r.Args))
	for _, a := range r.Args[1:] {
		present[a] = true
	}
	args := make([]string, 0, len(r.Args)+len(goCIParityFlags))
	args = append(args, r.Args[0])
	for _, f := range goCIParityFlags {
		if !present[f] {
			args = append(args, f)
		}
	}
	args = append(args, r.Args[1:]...)
	return Runner{Cmd: r.Cmd, Args: args, Dir: r.Dir, Deadline: r.Deadline}
}
