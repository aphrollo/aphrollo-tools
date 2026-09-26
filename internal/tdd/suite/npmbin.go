package suite

import (
	"encoding/json"
	"os"
	"path/filepath"
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
