package install

import (
	_ "embed"
	"path/filepath"
)

// `/aphrollo off|on|status` is the session's switch. The harness passes a slash
// command through to the prompt hook only when a skill of that name exists, so
// install writes one: the same managed file as the tdd skill, refreshed when the
// binary's copy moves on, and removed on uninstall only when it carries the
// marker this tool wrote.

//go:embed aphrolloskill.md
var aphrolloSkillBody string

// AphrolloSkill is the managed skill file's exact bytes.
func AphrolloSkill() string {
	return normalizeSkillBody(aphrolloSkillBody)
}

// aphrolloSkillPath is <configDir>/skills/aphrollo/SKILL.md.
func aphrolloSkillPath(configDir string) string {
	return filepath.Join(configDir, "skills", "aphrollo", "SKILL.md")
}

// WriteAphrolloSkill writes the managed `aphrollo` skill into a Claude config
// dir, reporting whether anything changed.
func WriteAphrolloSkill(configDir string) (bool, error) {
	return writeManagedSkill(aphrolloSkillPath(configDir), AphrolloSkill(), configDir)
}

// RemoveAphrolloSkill deletes the managed skill; a file without the marker is a
// user's own and is left alone.
func RemoveAphrolloSkill(configDir string) (bool, error) {
	return removeManagedSkill(aphrolloSkillPath(configDir), configDir)
}
