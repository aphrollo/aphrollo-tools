package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// registryHits answers a registry-both-ways law: every use must be registered
// AND every registry line must be used. Both directions are the point — a
// registry nobody prunes rots into a list of names that no longer exist.
// lastCapture is the last group of a match that captured anything.
func lastCapture(m []string) string {
	for i := len(m) - 1; i >= 1; i-- {
		if m[i] != "" {
			return m[i]
		}
	}
	return ""
}

func registryHits(root string, law Law, files []string, content map[string]string, applyScope, wholeTree bool) ([]Hit, error) {
	registryPath := filepath.Join(root, filepath.FromSlash(law.Matcher.RegistryFile))
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("law %q: reading registry %s: %w", law.Name, law.Matcher.RegistryFile, err)
	}
	registered := map[string]int{}
	var order []string
	for i, line := range splitLines(string(data)) {
		m := law.Matcher.EntryPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, seen := registered[m[1]]; !seen {
			order = append(order, m[1])
		}
		registered[m[1]] = i + 1
	}

	used := map[string]Hit{}
	var useOrder []string
	for _, rel := range files {
		if applyScope && !law.Scope.Matches(rel) {
			continue
		}
		for i, line := range splitLines(content[rel]) {
			for _, m := range law.Matcher.UsePattern.FindAllStringSubmatch(line, -1) {
				// An alternation carries one group per branch, and every branch
				// that did not match captured nothing: the LAST non-empty group
				// is the one that did.
				name := lastCapture(m)
				if name == "" {
					continue
				}
				if _, seen := used[name]; seen {
					continue
				}
				used[name] = Hit{File: rel, Line: i + 1}
				useOrder = append(useOrder, name)
			}
		}
	}

	var hits []Hit
	sort.Strings(useOrder)
	for _, name := range useOrder {
		if _, ok := registered[name]; ok {
			continue
		}
		at := used[name]
		hits = append(hits, Hit{
			Law: law.Name, File: at.File, Line: at.Line, Weight: 1,
			Key:  "unregistered | " + name,
			What: fmt.Sprintf("%s is used but not in %s", name, law.Matcher.RegistryFile),
		})
	}
	if !wholeTree {
		return hits, nil
	}
	sort.Strings(order)
	for _, name := range order {
		if _, ok := used[name]; ok {
			continue
		}
		hits = append(hits, Hit{
			Law: law.Name, File: law.Matcher.RegistryFile, Line: registered[name], Weight: 1,
			Key:  "stale | " + name,
			What: fmt.Sprintf("%s is registered but nothing uses it", name),
		})
	}
	return hits, nil
}
