package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// npmBinEntry is the script package pkgDir declares as its bin named name,
// "" when the package is absent or declares no such script. "bin" is a map
// of names, or one string for a package with a single command. A gate runs
// that script as `node <entry>`, never through npx or the .cmd shim npm
// writes on Windows.
func npmBinEntry(pkgDir, name string) string {
	var pkg struct {
		Bin json.RawMessage `json:"bin"`
	}
	// An absent or unreadable manifest decodes as nothing.
	data, _ := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	_ = json.Unmarshal(data, &pkg)
	var bins map[string]string
	if json.Unmarshal(pkg.Bin, &bins) != nil {
		var only string
		_ = json.Unmarshal(pkg.Bin, &only)
		bins = map[string]string{name: only}
	}
	rel := bins[name]
	entry := filepath.Join(pkgDir, filepath.FromSlash(rel))
	if _, err := os.Stat(entry); rel == "" || err != nil {
		return ""
	}
	return entry
}

// lookNodeFn finds the node binary. A variable, so a test names one without
// depending on the box's PATH.
var lookNodeFn = func() (string, error) { return exec.LookPath("node") }

// SetLookNodeForTest replaces lookNodeFn for a test and returns the restore.
func SetLookNodeForTest(fn func() (string, error)) (restore func()) {
	prev := lookNodeFn
	lookNodeFn = fn
	return func() { lookNodeFn = prev }
}

// nodeTestTools are the npm test tools DetectRunner runs through npx, in the
// order npmTestToolOf tries them.
var nodeTestTools = []string{"vitest", "jest"}

// npmTestToolOf is the npm test tool r runs, through npx or as
// `node <its bin entry>`; "" for any other runner. The entry is judged by
// the package directory it sits in, with either separator.
func npmTestToolOf(r Runner) string {
	if len(r.Args) == 0 {
		return ""
	}
	if r.Cmd == "npx" {
		return r.Args[0]
	}
	entry := strings.ReplaceAll(r.Args[0], `\`, "/")
	for _, tool := range nodeTestTools {
		if strings.Contains(entry, "/node_modules/"+tool+"/") {
			return tool
		}
	}
	return ""
}

// nodeToolRunner is r, an `npx vitest` or `npx jest` runner, rewritten to
// run root's installed copy of the tool as `node <its bin entry>` with the
// same arguments (issue #929): npx may reach the registry, and on Windows it
// goes through a .cmd shim and cmd.exe. Any other runner, `npm test --silent`
// included, comes back unchanged, and a runner already under node resolves
// to itself. When the tool is not installed or node is not on PATH, missing
// says which and r comes back unchanged, for the caller to report; nothing
// falls back to npx silently.
func nodeToolRunner(root string, r Runner) (_ Runner, missing string) {
	tool := npmTestToolOf(r)
	if !slices.Contains(nodeTestTools, tool) {
		return r, ""
	}
	entry := npmBinEntry(filepath.Join(root, "node_modules", tool), tool)
	if entry == "" {
		return r, fmt.Sprintf("%s is not installed in %s; run `npm ci` there", tool, filepath.Join(root, "node_modules"))
	}
	node, err := lookNodeFn()
	if err != nil {
		return r, "node is not on PATH; install Node.js to run " + tool
	}
	return Runner{Cmd: node, Args: append([]string{entry}, r.Args[1:]...), Dir: r.Dir}, ""
}
