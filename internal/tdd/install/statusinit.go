package install

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The statusline and the retired hook scripts are managed by the same rule as
// the session hooks: init owns what it wrote and what it supersedes, and
// leaves everything else alone.

// statusLineSub is the gate subcommand the badge runs.
const statusLineSub = "statusline"

// retiredStatusLineScripts are the statuslines aphrollo replaces. Either one
// left in place reports on a gate that is no longer installed, so init
// overwrites them; any OTHER command is the user's own and is kept.
var retiredStatusLineScripts = []string{"caveman-statusline.sh", "tdd-statusline.sh"}

// retiredHookNames are the exact leftovers from the Node plugin this binary
// replaced, beyond the tdd-*.js / tdd-*.sh families.
var retiredHookNames = []string{"tamper.js", "caveman-statusline.sh"}

// retiredHookGlobs are the leftover families. Scoped to `tdd-` prefixed
// scripts so a hook a user wrote is never in range.
var retiredHookGlobs = []string{"tdd-*.js", "tdd-*.sh"}

// statusLineCommandFor is the command init installs, quoted and
// slash-normalized exactly like the hook commands: these run through a shell,
// where a raw Windows path's backslashes are escapes.
func statusLineCommandFor(bin string) string {
	return fmt.Sprintf("%q %s %s", shellPath(bin), CmdName, statusLineSub)
}

// isManagedStatusLine reports whether a statusLine command is one this tool
// owns: its own, or one of the scripts it supersedes.
func isManagedStatusLine(cmd string) bool {
	for _, name := range []string{CmdName, LegacyCmdName} {
		if strings.Contains(cmd, name+" "+statusLineSub) {
			return true
		}
	}
	normalized := strings.ReplaceAll(cmd, `\`, "/")
	for _, script := range retiredStatusLineScripts {
		if strings.Contains(normalized, script) {
			return true
		}
	}
	return false
}

// patchStatusLine points root's statusLine at bin, preserving whatever else
// the block carried (padding and the like). A foreign command is left as it
// is: overwriting it would be init deleting a setting nobody asked it to.
func patchStatusLine(root map[string]any, bin string) {
	block := childMap(root, "statusLine")
	if cur, ok := block["command"].(string); ok && !isManagedStatusLine(cur) {
		return
	}
	block["type"] = "command"
	block["command"] = statusLineCommandFor(bin)
	root["statusLine"] = block
}

// stripStatusLine removes the managed statusLine, leaving a foreign one.
func stripStatusLine(root map[string]any) {
	block, ok := root["statusLine"].(map[string]any)
	if !ok {
		return
	}
	if cmd, ok := block["command"].(string); ok && !isManagedStatusLine(cmd) {
		return
	}
	delete(root, "statusLine")
}

// PruneRetiredHooks deletes the hook scripts this binary replaced from
// <configDir>/hooks, returning the names it removed so init reports each
// removal once. They are not inert: a settings.json entry still pointing at
// one double-fired every event, and a leftover statusline script reports on a
// gate that is not installed. A missing hooks dir is not an error.
func PruneRetiredHooks(configDir string) ([]string, error) {
	if configDir == "" {
		return nil, nil
	}
	dir := filepath.Join(configDir, "hooks")
	targets := map[string]bool{}
	for _, name := range retiredHookNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			targets[name] = true
		}
	}
	for _, glob := range retiredHookGlobs {
		matches, err := filepath.Glob(filepath.Join(dir, glob))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			targets[filepath.Base(m)] = true
		}
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	var removed []string
	for _, name := range names {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("removing %s: %w", filepath.Join(dir, name), err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}
