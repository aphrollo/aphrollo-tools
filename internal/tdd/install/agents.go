package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The gate's conduct assumes three agents exist: a builder that edits under
// the gate, a reviewer that reads a diff with no implementation context, and a
// researcher that only locates and traces. They ship with the binary for the
// same reason the tdd skill does — the rules they carry are the rules this
// binary enforces, and a copy installed by hand drifts from the enforcement
// the day either one changes.

// The templates sit beside the package's Go files rather than in an asset
// subdirectory: a directory holding only assets is not a Go package, and a
// gate that maps a staged file to its own directory asks `go test` for one
// that does not exist.
//
//go:embed agent_*.md
var agentTemplates embed.FS

// agentMarker identifies a file this tool wrote, so uninstall never deletes an
// agent of the same name that a user wrote themselves.
const agentMarker = tddSkillMarker

// managedAgentNames is the installed set, in a fixed order so init's output
// and a doctor check read the same way every run.
var managedAgentNames = []string{"builder", "researcher", "reviewer"}

// ManagedAgent is one managed agent's exact bytes, ok=false for a name this
// tool does not ship. Line endings are normalized for the same reason the
// skill's are: go:embed reads raw bytes, and a CRLF checkout would otherwise
// install a different file on every box.
func ManagedAgent(name string) (string, bool) {
	data, err := agentTemplates.ReadFile("agent_" + name + ".md")
	if err != nil {
		return "", false
	}
	return normalizeSkillBody(string(data)), true
}

// agentPath is <configDir>/agents/<name>.md.
func agentPath(configDir, name string) string {
	return filepath.Join(configDir, "agents", name+".md")
}

// WriteAgents installs the managed agents into a Claude config dir, returning
// the names whose bytes changed: a second run over the same binary writes
// nothing. A file that is already byte-identical is left alone, mtime and all.
func WriteAgents(configDir string) ([]string, error) {
	if configDir == "" {
		return nil, nil
	}
	var written []string
	for _, name := range managedAgentNames {
		want, ok := ManagedAgent(name)
		if !ok {
			return written, fmt.Errorf("no embedded agent template named %q", name)
		}
		path := agentPath(configDir, name)
		if have, err := os.ReadFile(path); err == nil && string(have) == want {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return written, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			return written, fmt.Errorf("writing %s: %w", path, err)
		}
		written = append(written, name)
	}
	sort.Strings(written)
	return written, nil
}

// RemoveAgents deletes the managed agents, returning the names it removed. A
// file without the marker was written by someone else and is left alone.
func RemoveAgents(configDir string) ([]string, error) {
	if configDir == "" {
		return nil, nil
	}
	var removed []string
	for _, name := range managedAgentNames {
		path := agentPath(configDir, name)
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("reading %s: %w", path, err)
		}
		if !strings.Contains(string(body), agentMarker) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", path, err)
		}
		removed = append(removed, name)
	}
	sort.Strings(removed)
	return removed, nil
}
