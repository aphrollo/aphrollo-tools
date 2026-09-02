package tdd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// managedEvent is one Claude Code hook event aphrollo tdd installs into
// settings.json, with the matcher (empty = all) and the tdd subcommand it runs.
type managedEvent struct {
	event   string
	matcher string
	sub     string
	timeout int
}

// postToolUseHarnessMarginSecs is the slack ABOVE DefaultPostEditTimeout that
// the PostToolUse hook-template timeout (settings.json's own "timeout" field)
// must carry. The Claude Code harness kills the hook PROCESS from OUTSIDE
// once ITS timeout elapses, entirely independent of RunSuite's Go-side
// context deadline — if the template timeout is shorter than (or too close
// to) DefaultPostEditTimeout, the harness kills the hook BEFORE Go's own
// deadline (and its 2s WaitDelay cleanup) ever fires: no TIMEOUT line gets
// returned, no state gets stamped, and the spawned cargo process is left
// orphaned (the harness's kill reaches only the direct hook process, never
// RunSuite's own child cleanup, which needs ITS deadline to fire first).
// Found in review 2026-08-15: the template had drifted to a hardcoded 90s
// while DefaultPostEditTimeout had already moved to 100s. 20s covers the 2s
// WaitDelay plus real scheduling slack.
const postToolUseHarnessMarginSecs = 20

// postToolUseHarnessTimeoutSecs is DERIVED from DefaultPostEditTimeout (the
// tdd package's single source of truth for the Go-side PostToolUse budget —
// also aliased by cli.go's postEditTimeout) so the two can never silently
// drift apart again the way they did before this fix.
const postToolUseHarnessTimeoutSecs = int(DefaultPostEditTimeout/time.Second) + postToolUseHarnessMarginSecs

// managedEvents is the canonical set of session hooks `aphrollo tdd init`
// wires. Order is stable so the marshaled settings.json is deterministic.
var managedEvents = []managedEvent{
	{"SessionStart", "", "sessionstart", 10},
	{"PreToolUse", "Edit|Write|MultiEdit|NotebookEdit", "pretooluse", 10},
	{"PostToolUse", "Edit|Write|MultiEdit", "posttooluse", postToolUseHarnessTimeoutSecs},
	{"UserPromptSubmit", "", "userpromptsubmit", 10},
	{"SessionEnd", "", "sessionend", 10},
}

// legacyCmdMarkers identify a claude-code-tdd Node-plugin hook command so init
// removes it regardless of the path it is installed at. A foreign command
// (e.g. caveman) matches none of these.
var legacyCmdMarkers = []string{"/hooks/tdd-", "claude-code-tdd"}

// isManagedCmd reports whether a hook command is one this tool owns. It matches
// on the `tdd <subcommand>` invocation rather than the binary name, so a patch
// recognises (and replaces) its own entries no matter what path the aphrollo
// binary lives at — os.Executable in tests, /usr/local/bin/aphrollo in prod, or
// a renamed install. Node-plugin entries match too, so init removes them.
func isManagedCmd(cmd string) bool {
	for _, me := range managedEvents {
		for _, name := range []string{CmdName, LegacyCmdName} {
			if strings.Contains(cmd, name+" "+me.sub) {
				return true
			}
		}
	}
	// Legacy markers are written with forward slashes; a Windows install's
	// command embeds the same path with backslashes (`…\hooks\tdd-post-edit.js`),
	// which left the node hooks in place beside ours — every event double-fired.
	// Normalize before matching so one marker spelling covers both.
	normalized := strings.ReplaceAll(cmd, `\`, "/")
	for _, m := range legacyCmdMarkers {
		if strings.Contains(normalized, m) {
			return true
		}
	}
	return false
}

// PatchSettings injects the aphrollo tdd session hooks into a settings.json
// document, replacing any prior tdd-managed entries (including Node-plugin
// hooks) and preserving every foreign hook and top-level key. It returns the
// new document, whether anything changed, and an error only on malformed input.
// The output is deterministic, so a second patch over it is a no-op.
func PatchSettings(existing []byte, bin string) ([]byte, bool, error) {
	root, err := parseSettings(existing)
	if err != nil {
		return nil, false, err
	}
	before, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}

	hooks := childMap(root, "hooks")
	for _, me := range managedEvents {
		kept := stripManaged(toGroups(hooks[me.event]))
		hooks[me.event] = append(kept, me.group(bin))
	}
	root["hooks"] = hooks

	after, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}
	return after, !bytes.Equal(before, after), nil
}

// StripSettings removes every aphrollo tdd (and legacy Node tdd) session hook,
// dropping events left empty, while leaving foreign hooks and other keys alone.
func StripSettings(existing []byte) ([]byte, bool, error) {
	root, err := parseSettings(existing)
	if err != nil {
		return nil, false, err
	}
	before, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}

	hooks := childMap(root, "hooks")
	for _, me := range managedEvents {
		groups, ok := hooks[me.event]
		if !ok {
			continue
		}
		kept := stripManaged(toGroups(groups))
		if len(kept) == 0 {
			delete(hooks, me.event)
		} else {
			hooks[me.event] = kept
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}

	after, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}
	return after, !bytes.Equal(before, after), nil
}

// InitSettings installs (or, with uninstall=true, removes) the aphrollo tdd
// session hooks in configDir/settings.json, pointing them at the bin path. It
// creates the file when installing into a fresh dir, backs up any existing file
// before rewriting it, and is a no-op when nothing would change. Returns whether
// the file was modified.
func InitSettings(configDir, bin string, uninstall bool) (bool, error) {
	path := filepath.Join(configDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	existed := err == nil

	var out []byte
	var changed bool
	if uninstall {
		if !existed {
			return false, nil
		}
		out, changed, err = StripSettings(existing)
	} else {
		out, changed, err = PatchSettings(existing, bin)
	}
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}

	if existed {
		backup := fmt.Sprintf("%s.pre-tdd-%d", path, time.Now().Unix())
		if err := os.WriteFile(backup, existing, 0o644); err != nil {
			return false, fmt.Errorf("writing backup %s: %w", backup, err)
		}
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// group renders the canonical hook group for this event.
func (me managedEvent) group(bin string) any {
	g := map[string]any{
		"hooks": []any{map[string]any{
			"type": "command",
			// Slash-normalized + quoted like the git shims: hook commands run
			// through a shell, where a raw Windows path's backslashes are
			// escapes — the session hooks died "command not found" live.
			"command": fmt.Sprintf("%q %s %s", shellPath(bin), CmdName, me.sub),
			"timeout": me.timeout,
		}},
	}
	if me.matcher != "" {
		g["matcher"] = me.matcher
	}
	return g
}

// stripManaged drops every tdd-managed hook command from each group, removing a
// group entirely once it holds no hooks. Foreign groups pass through untouched.
func stripManaged(groups []any) []any {
	var out []any
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			out = append(out, g)
			continue
		}
		hs, _ := gm["hooks"].([]any)
		var kept []any
		for _, h := range hs {
			if hm, ok := h.(map[string]any); ok {
				if cmd, ok := hm["command"].(string); ok && isManagedCmd(cmd) {
					continue
				}
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			continue
		}
		gm["hooks"] = kept
		out = append(out, gm)
	}
	return out
}

func parseSettings(existing []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(existing, &m); err != nil {
		return nil, fmt.Errorf("settings.json is not valid JSON: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func marshalSettings(m map[string]any) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// childMap returns root[key] as a map, creating it if absent or the wrong type.
func childMap(root map[string]any, key string) map[string]any {
	if m, ok := root[key].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func toGroups(v any) []any {
	g, _ := v.([]any)
	return g
}
