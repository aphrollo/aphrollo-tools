package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// tableCell splits a `|`-delimited markdown table row and returns the col-th
// cell (0-based, trimmed), after a leading and trailing `|` are stripped —
// the shape a GFM table row is written in. ok is false when the row does not
// carry that many cells, which lets a caller skip a line that is not really
// a table row (the separator row, a stray `|` in prose) rather than reading
// the wrong column out of it.
func tableCell(line string, col int) (string, bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	if trimmed == "" {
		return "", false
	}
	cells := strings.Split(trimmed, "|")
	if col < 0 || col >= len(cells) {
		return "", false
	}
	return strings.TrimSpace(cells[col]), true
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
		text := line
		if law.Matcher.HasEntryColumn {
			cell, ok := tableCell(line, law.Matcher.EntryColumn)
			if !ok {
				continue
			}
			text = cell
		}
		m := law.Matcher.EntryPattern.FindStringSubmatch(text)
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
