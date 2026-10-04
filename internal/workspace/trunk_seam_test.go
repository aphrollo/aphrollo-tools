package workspace

// Most tests here run a verb over a worktree that is only a path on a stub's
// lips, with no repository behind it to name a trunk. They get "main"; the
// tests of the trunk read itself put the real read back with realTrunk.
var realTrunk = trunkOf

func init() {
	trunkOf = func(repo string) string {
		if t := realTrunk(repo); t != "" {
			return t
		}
		return "main"
	}
}
