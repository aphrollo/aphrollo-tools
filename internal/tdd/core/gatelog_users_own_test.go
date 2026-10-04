package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// gate.log is retired: every reader of the gate's history reads the v1 events.
// Nothing writes the file any more; the one fallback reader of the pre-upgrade
// history stays until the date named in gatelog_legacy.go. The set of non-test
// files that name it or its path is pinned here. A new one is a second log
// growing back: carry the field on the event instead.
func TestGateLog_OnlyTheLegacyWriterAndTheNamedReadersTouchTheFile(t *testing.T) {
	allowed := map[string]string{
		"internal/tdd/core/gatelog_legacy.go": "the one-time fallback for history older than the complete events",
		"internal/tdd/core/stats_core.go":     "GateLogPath, the path itself",
		"internal/tdd/core/stateschema.go":    "the schema stamp of the legacy file",
		"tools/replay/legs.go":                "stat of the previous release's file",
	}
	// tree-read-ok: the pin is a statement about the source tree itself.
	root := filepath.Join("..", "..", "..")
	names := regexp.MustCompile(`"gate\.log"|\bGateLogPath\b`)
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "scratchpad" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if names.Match(data) {
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	seen := map[string]bool{}
	for _, f := range found {
		seen[f] = true
		if _, ok := allowed[f]; !ok {
			t.Errorf("%s names gate.log; read or write the v1 events instead (a new field on the one writer)", f)
		}
	}
	for f, why := range allowed {
		if !seen[f] {
			t.Errorf("%s (%s) no longer names gate.log: drop it from the allowlist", f, why)
		}
	}
}
