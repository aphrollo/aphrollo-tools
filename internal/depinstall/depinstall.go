// Package depinstall owns the one rule for installing a checkout's
// dependencies: which marker file selects which install command, and which
// directory means the install already happened. `workspace create` runs it in
// a fresh lane; the PR merge gate runs it in the throwaway checkout it builds
// the merge in. Neither carries a copy of the table.
package depinstall

import (
	"os"
	"path/filepath"
)

// Rule maps a marker file in a project root to the install command and the
// directory whose presence means "already installed".
type Rule struct {
	Marker  string   // file whose presence selects this rule
	Argv    []string // install command, run with the project root as cwd
	Present string   // dir under the root that means deps are already there ("" => never skip)
}

// Rules is the install table. Order matters: the first matching marker wins,
// so a pnpm-lock beats a bare package.json.
var Rules = []Rule{
	{Marker: "pnpm-lock.yaml", Argv: []string{"pnpm", "install"}, Present: "node_modules"},
	{Marker: "yarn.lock", Argv: []string{"yarn", "install"}, Present: "node_modules"},
	{Marker: "package-lock.json", Argv: []string{"npm", "ci"}, Present: "node_modules"},
	{Marker: "package.json", Argv: []string{"npm", "install"}, Present: "node_modules"},
	{Marker: "go.mod", Argv: []string{"go", "mod", "download"}, Present: ""},
}

// Detect picks the install rule for a project root by probing for marker
// files in priority order. ok is false when nothing matches.
func Detect(root string) (Rule, bool) {
	for _, r := range Rules {
		if fileExists(filepath.Join(root, r.Marker)) {
			return r, true
		}
	}
	return Rule{}, false
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
