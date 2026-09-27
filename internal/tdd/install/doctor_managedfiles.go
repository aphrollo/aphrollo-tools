package install

import (
	"os"
	"path/filepath"
	"strings"
)

// doctorManagedFiles checks every skill and agent this binary ships is present
// and byte-identical to the template it carries. A drifted copy means a
// session follows rules the gate does not enforce.
//
// A file counts when either Claude config dir a session loads it from holds
// the current copy: the user-level one, or the repo's own .claude, which a
// project-scoped install (--config-dir <repo>/.claude) writes (issue #887).
func doctorManagedFiles(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "managed skills and agents"}
	dirs := []string{in.ConfigDir}
	if project := projectClaudeDir(in.Repo); project != "" {
		dirs = append(dirs, project)
	}
	want := map[string]string{
		"tdd/SKILL.md": TDDSkill(),
		"sdd/SKILL.md": SDDSkill(),
	}
	paths := map[string]func(string) string{
		"tdd/SKILL.md": tddSkillPath,
		"sdd/SKILL.md": sddSkillPath,
	}
	for _, name := range managedAgentNames {
		body, ok := ManagedAgent(name)
		if !ok {
			continue
		}
		want["agents/"+name+".md"] = body
		paths["agents/"+name+".md"] = func(dir string) string { return agentPath(dir, name) }
	}
	var stale []string
	for label, body := range want {
		if state := managedFileState(dirs, paths[label], body); state != "" {
			stale = append(stale, label+" ("+state+")")
		}
	}
	if len(stale) > 0 {
		sortStrings(stale)
		c.Detail = strings.Join(stale, ", ") + " — run `aphrollo install`"
		return c
	}
	c.OK = true
	return c
}

// managedFileState is "" when some dir holds the current copy of a managed
// file, else "edited" when a dir holds a copy that differs, else "missing".
func managedFileState(dirs []string, pathIn func(string) string, body string) string {
	state := "missing"
	for _, dir := range dirs {
		have, err := os.ReadFile(pathIn(dir))
		switch {
		case err != nil:
			continue
		case string(have) == body:
			return ""
		}
		state = "edited"
	}
	return state
}

// projectClaudeDir is the repo's own .claude dir, where Claude Code loads a
// project's skills and agents from; "" when repo is not inside a git
// checkout.
func projectClaudeDir(repo string) string {
	if repo == "" {
		return ""
	}
	top, err := gitRead(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Join(strings.TrimSpace(top), ".claude")
}
