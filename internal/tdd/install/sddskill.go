package install

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The `tdd` skill says how to write one change; this one says how a feature
// gets from a question to merged code: brainstorm to a spec, plan into lanes,
// one lane per builder under the tdd skill, then close the transient tree out
// into the durable homes and delete it. It ships with the binary for the same
// reason the tdd skill does — it names the gate's own artefacts, and a copy
// installed by hand drifts the day either changes.

//go:embed sddskill.md
var sddSkillBody string

// SDDSkill is the managed skill file's exact bytes.
func SDDSkill() string {
	return normalizeSkillBody(sddSkillBody)
}

// sddSkillPath is <configDir>/skills/sdd/SKILL.md.
func sddSkillPath(configDir string) string {
	return filepath.Join(configDir, "skills", "sdd", "SKILL.md")
}

// WriteSDDSkill writes the managed `sdd` skill into a Claude config dir,
// reporting whether anything changed: a second run over the same binary writes
// nothing.
func WriteSDDSkill(configDir string) (bool, error) {
	return writeManagedSkill(sddSkillPath(configDir), SDDSkill(), configDir)
}

// RemoveSDDSkill deletes the managed skill. A file without the marker was
// written by someone else and is left alone.
func RemoveSDDSkill(configDir string) (bool, error) {
	return removeManagedSkill(sddSkillPath(configDir), configDir)
}

// writeManagedSkill is the shared body of every managed skill writer: create
// the directory, write only when the bytes differ, report whether it wrote.
func writeManagedSkill(path, want, configDir string) (bool, error) {
	if configDir == "" {
		return false, nil
	}
	if have, err := os.ReadFile(path); err == nil && string(have) == want {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// removeManagedSkill deletes a managed skill file, leaving one that carries no
// marker: an unmarked file of the same name is a user's own.
func removeManagedSkill(path, configDir string) (bool, error) {
	if configDir == "" {
		return false, nil
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	if !strings.Contains(string(body), tddSkillMarker) {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("removing %s: %w", path, err)
	}
	return true, nil
}
