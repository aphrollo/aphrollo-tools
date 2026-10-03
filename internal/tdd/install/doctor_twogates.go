package install

import (
	"fmt"
	"sort"
	"strings"
)

// Two gates on one repo judge every edit twice and disagree about the
// verdict. trellis replaces aphrollo's hooks; until a box has moved over, the
// two can both be wired into the same Claude settings. aphrollo stands down in
// a repo that holds trellis.toml, but a box wired both ways everywhere else
// still runs two gates, so doctor names it with the fix.

// doctorOneLiveGate fails when the managed aphrollo hooks and a trellis hook
// or enabled trellis plugin are wired into the same settings.json. A config
// with no managed hooks, or one that cannot be read, is some other check's
// finding, never this one's.
func doctorOneLiveGate(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "one live gate", OK: true}
	root, err := readSettings(in.ConfigDir)
	if err != nil || len(managedHookBinaries(in.ConfigDir)) == 0 {
		return c
	}
	found := trellisWiring(root)
	if len(found) == 0 {
		return c
	}
	c.OK = false
	c.Detail = fmt.Sprintf("aphrollo hooks and trellis (%s) are both wired in %s, so two gates judge the same edit; "+
		"keep one: disable the trellis plugin, or remove the aphrollo hooks with `aphrollo gate init --uninstall`",
		strings.Join(found, ", "), in.ConfigDir)
	return c
}

// trellisWiring lists what in a parsed settings.json runs trellis: each
// enabled plugin named for it, and each hook event holding a hook command that
// mentions it. Sorted, so two runs read the same.
func trellisWiring(root map[string]any) []string {
	var found []string
	plugins, _ := root["enabledPlugins"].(map[string]any)
	for name, on := range plugins {
		if enabled, _ := on.(bool); enabled && strings.Contains(strings.ToLower(name), "trellis") {
			found = append(found, "plugin "+name)
		}
	}
	hooks, _ := root["hooks"].(map[string]any)
	for event, groups := range hooks {
		if groupsMention(groups, "trellis") {
			found = append(found, "a "+event+" hook")
		}
	}
	sort.Strings(found)
	return found
}

// groupsMention reports whether any hook command under one event's groups
// contains word, ignoring case. A command this tool wrote is never counted: its
// binary path may hold the word without being the other gate.
func groupsMention(groups any, word string) bool {
	for _, g := range toGroups(groups) {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); !isManagedCmd(cmd) && strings.Contains(strings.ToLower(cmd), word) {
				return true
			}
		}
	}
	return false
}
