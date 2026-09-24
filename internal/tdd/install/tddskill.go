package install

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The gate enforces the RED→GREEN outcome mechanically; the nuances it cannot
// check -- what a test is worth, how an expectation is derived, what counts as
// evidence -- lived in a plugin skill that is no longer installed. They now
// ship with the binary that enforces the outcome, as a user-level skill `gate
// init` writes, so the procedure and the gate can never drift apart.

//go:embed tddskill.md
var tddSkillBody string

// tddSkillMarker identifies a file this tool wrote, so uninstall never deletes
// a user's own skill of the same name.
const tddSkillMarker = "Written by `aphrollo install`"

// TDDSkill is the managed skill file's exact bytes. Line endings are
// normalized: a Windows checkout with core.autocrlf=true embeds the template
// with CRLF, and the installed file must be byte-identical whatever the
// checkout did to the source.
func TDDSkill() string {
	return normalizeSkillBody(tddSkillBody)
}

// normalizeSkillBody renders an embedded managed template as LF text with
// exactly one trailing newline.
func normalizeSkillBody(body string) string {
	return strings.TrimSuffix(strings.ReplaceAll(body, "\r\n", "\n"), "\n") + "\n"
}

// tddSkillPath is <configDir>/skills/tdd/SKILL.md.
func tddSkillPath(configDir string) string {
	return filepath.Join(configDir, "skills", "tdd", "SKILL.md")
}

// resolvedTDDSkillPath is the ONE place that answers "where is the tdd skill,
// and is it actually there": the session-start nudge, the managed CLAUDE.md
// block, and this file's own WriteTDDSkill all resolve through it, so a
// writer and its readers can never name three different files. installed is
// false both when the path cannot be resolved (no home dir) and when nothing
// is written there yet — either way a caller must not print path as if it
// exists.
func resolvedTDDSkillPath() (path string, installed bool) {
	path = tddSkillPath(claudeConfigDir())
	if path == "" {
		return "", false
	}
	info, err := os.Stat(path)
	return path, err == nil && !info.IsDir()
}

// WriteTDDSkill writes the managed `tdd` skill into a Claude config dir and
// retires the slash-command stub it replaces. It reports whether anything
// changed: a second run over the same binary writes nothing.
func WriteTDDSkill(configDir string) (bool, error) {
	if configDir == "" {
		return false, nil
	}
	retired, err := retireTDDCommand(configDir)
	if err != nil {
		return false, err
	}
	path := tddSkillPath(configDir)
	want := TDDSkill()
	if have, err := os.ReadFile(path); err == nil && string(have) == want {
		return retired, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return retired, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return retired, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// RemoveTDDSkill deletes the managed skill. A file without the marker was
// written by someone else and is left alone.
func RemoveTDDSkill(configDir string) (bool, error) {
	if configDir == "" {
		return false, nil
	}
	path := tddSkillPath(configDir)
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

// retireTDDCommand deletes the `/tdd` command stub this tool used to write.
// The skill carries the same name, so `/tdd` keeps working; leaving both would
// define the command twice. A stub that never mentioned aphrollo is a user's.
func retireTDDCommand(configDir string) (bool, error) {
	path := filepath.Join(configDir, "commands", "tdd.md")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	if !strings.Contains(string(body), "aphrollo") {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("removing %s: %w", path, err)
	}
	return true, nil
}
