package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gate.log is retired: every reader of the gate's history reads the v1 events.
// The file is still written, for the pre-merge readers of internal/tdd/merge
// that have not moved (the host-port lane owns them), so the set of non-test
// files that name it or its path is pinned here. A new one is a second log
// growing back: carry the field on the event instead.
func TestGateLog_OnlyTheLegacyWriterAndTheNamedReadersTouchTheFile(t *testing.T) {
	allowed := map[string]string{
		"internal/tdd/core/state.go":             "the legacy writer",
		"internal/tdd/core/stats_core.go":        "GateLogPath, the path itself",
		"internal/tdd/core/stateschema.go":       "the schema stamp of the legacy file",
		"internal/tdd/merge/commitmsg_verify.go": "reader: last precommit verdict",
		"internal/tdd/merge/localci.go":          "reader: stored green of a merge tree",
		"internal/tdd/merge/retro.go":            "reader: the retro lane scan",
		"internal/tdd/merge/deps_core.go":        "generated alias of GateLogPath",
		"internal/tdd/api_core.go":               "generated alias of GateLogPath",
		"internal/tdd/internal/tddtest/files.go": "test support",
		"tools/replay/legs.go":                   "stat of the previous release's file",
	}
	// tree-read-ok: the pin is a statement about the source tree itself.
	root := filepath.Join("..", "..", "..")
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
		if strings.Contains(string(data), `"gate.log"`) || strings.Contains(string(data), "GateLogPath()") {
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	for _, f := range found {
		if strings.HasPrefix(f, "internal/workspace/") {
			continue
		}
		if _, ok := allowed[f]; !ok {
			t.Errorf("%s names gate.log; read or write the v1 events instead (a new field on the one writer)", f)
		}
	}
}
