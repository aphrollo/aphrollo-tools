package cli

// Verb is one entry in a command-dispatch table: its name, and — if it is a
// retiring alias for another verb rather than the canonical spelling — which
// one it points at.
//
// These three tables are the single source both CLAUDE.md's "Command
// surface" section and this file's own tests read. They are hand-kept
// alongside cli.go's, cli_gate.go's and cli_workspace.go's own dispatch
// rather than generated from it: Run's top-level switch calls handlers with
// different signatures (guardrail and gate need stdin, the rest don't), and
// gate's dispatch is an if-chain that does real, sometimes side-effecting
// work per branch (a git hook, a mutation job, a toolchain shim) rather than
// a uniform "name -> func(args) int" mapping — turning either into a
// name-keyed table would risk changing what a branch does, not just how it
// is spelled. TestSurface_TablesMatchDispatch is what proves this file and
// the real dispatch still agree, so the two cannot silently drift apart.
type Verb struct {
	Name  string
	Alias bool
	Of    string // canonical verb this one is a retiring alias for; "" if none
}

// topLevelVerbTable mirrors Run's switch in cli.go, in the order it reads.
var topLevelVerbTable = []Verb{
	{Name: "refactor"},
	{Name: "find"},
	{Name: "outline"},
	{Name: "show"},
	{Name: "workspace"},
	{Name: "dev"},
	{Name: "guardrail"},
	{Name: "gate"},
	{Name: "tdd", Alias: true, Of: "gate"},
	{Name: "install"},
	{Name: "issue"},
	{Name: "ratchet"},
	{Name: "sqlc"},
	{Name: "docs"},
	{Name: "check"},
	{Name: "version"},
	{Name: "update"},
}

// gateVerbTable mirrors runGate's if-chain and its trailing hook switch in
// cli_gate.go, in the order it reads.
var gateVerbTable = []Verb{
	{Name: "install", Alias: true, Of: "install"},
	{Name: "init", Alias: true, Of: "install"},
	{Name: "primary-edits", Alias: true, Of: "allow/revoke"},
	{Name: "allow"},
	{Name: "revoke"},
	{Name: "commitmsg"},
	{Name: "postcommit"},
	{Name: "postmerge"},
	{Name: "doctor"},
	{Name: "statusline"},
	{Name: "stats"},
	{Name: "output"},
	{Name: "gc"},
	{Name: "issue", Alias: true, Of: "issue"},
	{Name: "feedback"},
	{Name: "escape"},
	{Name: "runphase"},
	{Name: "mutants"},
	{Name: "cargo"},
	{Name: "git"},
	{Name: "precommit"},
	{Name: "premergecommit", Alias: true, Of: "premerge"},
	{Name: "premerge"},
	{Name: "prepush"},
	{Name: "sessionstart"},
	{Name: "pretooluse"},
	{Name: "posttooluse"},
	{Name: "userpromptsubmit"},
	{Name: "sessionend"},
}

// workspaceVerbTable mirrors runWorkspace's switch in cli_workspace.go, in
// the order it reads.
var workspaceVerbTable = []Verb{
	{Name: "create"},
	{Name: "claim"},
	{Name: "unclaim"},
	{Name: "list"},
	{Name: "remove"},
	{Name: "prune"},
	{Name: "commit"},
	{Name: "push"},
	{Name: "pr"},
	{Name: "ship"},
	{Name: "submit"},
	{Name: "status"},
	{Name: "diff"},
	{Name: "update", Alias: true, Of: "rebase"},
	{Name: "rebase"},
	{Name: "sync"},
	{Name: "verify", Alias: true, Of: "check"},
	{Name: "merge"},
}

// verbNames projects a Verb table down to its names, in table order.
func verbNames(t []Verb) []string {
	names := make([]string, len(t))
	for i, v := range t {
		names[i] = v.Name
	}
	return names
}

// TopLevelVerbs lists every command Run dispatches, in the order the switch
// reads them.
func TopLevelVerbs() []string { return verbNames(topLevelVerbTable) }

// GateVerbs lists every subcommand runGate dispatches, in the order the
// if-chain and hook switch read them.
func GateVerbs() []string { return verbNames(gateVerbTable) }

// WorkspaceVerbs lists every subcommand runWorkspace dispatches, in the order
// the switch reads them.
func WorkspaceVerbs() []string { return verbNames(workspaceVerbTable) }
