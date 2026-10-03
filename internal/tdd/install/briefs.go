package install

// Brief is one text this tool installs that a session pays tokens for: the
// managed CLAUDE.md block, the tdd skill, or an agent's own brief.
type Brief struct {
	Name     string
	Subagent bool // an agent's own brief, which carries the lower token cap
	Text     string
}

// Briefs are the texts `install` writes for the repo at repoRoot, exactly as
// written: the block rendered with the flags that repo declares, the skill, and
// each managed agent. `aphrollo stats --briefs` measures them against the caps.
func Briefs(repoRoot string) []Brief {
	out := []Brief{
		{Name: "managed CLAUDE.md block", Text: managedBlockFor(repoRoot)},
		{Name: "tdd skill", Text: TDDSkill()},
	}
	for _, name := range managedAgentNames {
		if text, ok := ManagedAgent(name); ok {
			out = append(out, Brief{Name: "agent " + name, Subagent: true, Text: text})
		}
	}
	return out
}
