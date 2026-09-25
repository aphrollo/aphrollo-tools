package ratchet

import (
	"path/filepath"
	"strings"
)

// packageMarkerHits answers a marker-in-package law: a trigger match is a
// hit only when NEITHER its own file NOR any other file beside it (same
// directory, in the law's own scope) carries `marker` anywhere. A hit here
// depends on content HitsIn never sees — a sibling file's whole text — so
// it is answered here, the way registry-both-ways answers its own
// whole-scope question, rather than in the per-file, pure HitsIn path.
func packageMarkerHits(view treeView, law Law, files []string, content map[string]string) ([]Hit, error) {
	siblingsByDir := map[string][]string{}
	var hits []Hit
	for _, rel := range files {
		if !law.Scope.Matches(rel) {
			continue
		}
		text, ok := content[rel]
		if !ok {
			continue
		}
		fl := newFileLines(rel, text)
		raw := fl.raw
		code := fl.codeFor(law)
		var candidates []int
		for i, line := range code {
			if law.excluded(line) || !law.Matcher.Trigger.MatchString(line) || law.escaped(rel, raw, i) {
				continue
			}
			candidates = append(candidates, i)
		}
		if len(candidates) == 0 {
			continue
		}
		if law.Matcher.Marker.MatchString(strings.Join(code, "\n")) {
			continue // excused by its own file
		}
		dir := packageDirOf(rel)
		siblings, ok := siblingsByDir[dir]
		if !ok {
			var err error
			if siblings, err = packageSiblings(view, law, dir); err != nil {
				return nil, err
			}
			siblingsByDir[dir] = siblings
		}
		excused := false
		for _, sib := range siblings {
			if sib == rel {
				continue
			}
			sibText, err := siblingText(view, sib, content)
			if err != nil {
				return nil, err
			}
			sibCode := newFileLines(sib, sibText).codeFor(law)
			if law.Matcher.Marker.MatchString(strings.Join(sibCode, "\n")) {
				excused = true
				break
			}
		}
		if excused {
			continue
		}
		for _, i := range candidates {
			hits = append(hits, law.hit(rel, i+1, strings.TrimSpace(raw[i])))
		}
	}
	return hits, nil
}

// packageDirOf is the repo-relative directory a slash path lives in, "" for
// one at the repo root — the "package" a marker-in-package law groups by.
func packageDirOf(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// packageSiblings lists every file directly inside dir that the law's own
// scope matches — the candidates a trigger's excuse may live in. Read from
// disk rather than the walk's own file list: a narrowed pre-edit run visits
// only the one file being written and never its siblings otherwise, and the
// TestMain declaring a package's isolation is exactly one of those siblings.
// A file on disk the view does not carry (untracked, at commit time) is no
// sibling: it lands with no commit, so it excuses nothing in one.
func packageSiblings(view treeView, law Law, dir string) ([]string, error) {
	entries, err := readDir(filepath.Join(view.root, filepath.FromSlash(dir)))
	if err != nil {
		if vanished(err) {
			return nil, nil // absence-ok: a dir that vanished mid-walk has no siblings to offer, not an error
		}
		return nil, &ScanReadError{Path: dir, Err: err}
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rel := path(dir, e.Name())
		if law.Scope.Matches(rel) && view.has(rel) {
			out = append(out, rel)
		}
	}
	return out, nil
}

// siblingText is a sibling file's content: whatever the walk already loaded
// (honoring a `--proposed` overlay when the sibling is itself being edited),
// or a direct disk read otherwise. A sibling that vanished mid-walk excuses
// nothing rather than failing the whole law.
func siblingText(view treeView, rel string, content map[string]string) (string, error) {
	if text, ok := content[rel]; ok {
		return text, nil
	}
	data, err := view.read(rel)
	if err != nil {
		if vanished(err) {
			return "", nil // absence-ok: a sibling that vanished mid-walk offers no excuse, not an error
		}
		return "", &ScanReadError{Path: rel, Err: err}
	}
	return string(data), nil
}
