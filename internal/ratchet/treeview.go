package ratchet

import "path/filepath"

// treeView is the tree a whole-tree matcher reads beyond the scan's own
// content map: a registry file, a superset file, a package sibling, a bench
// record. It is the SAME view the scan judged every other file from — the
// overlay over the disk, restricted to the tracked set when the caller passed
// one — so the two sides of a two-sided law never come from different trees.
// At commit time the overlay is the index and the tracked set is `git
// ls-files`, which together are the staged tree.
type treeView struct {
	root    string
	overlay map[string]string
	tracked map[string]bool // nil: every file on disk is in the view
}

// diskView is the tree exactly as it sits on disk: what adoption and a
// fixture directory judge.
func diskView(root string) treeView { return treeView{root: root} }

// viewOf is the view a Check run judges.
func viewOf(opts Options) treeView {
	v := treeView{root: opts.Root, overlay: opts.Proposed}
	if len(opts.Tracked) > 0 {
		v.tracked = map[string]bool{}
		for _, rel := range opts.Tracked {
			v.tracked[normalizeSlashes(rel)] = true
		}
	}
	return v
}

// read is rel's content in this view: the overlay's when it carries rel,
// else the file on disk.
func (v treeView) read(rel string) ([]byte, error) {
	if text, ok := v.overlay[rel]; ok {
		return []byte(text), nil
	}
	return readFile(filepath.Join(v.root, filepath.FromSlash(rel)))
}

// has reports whether a file found on disk at rel is part of this view. An
// untracked file is part of no commit, so it excuses and measures nothing.
func (v treeView) has(rel string) bool {
	return v.tracked == nil || v.tracked[rel]
}

// viewGlobFiles is globFiles over the view's root, keeping only the files the
// view carries.
func viewGlobFiles(view treeView, glob string) ([]string, error) {
	files, err := globFiles(view.root, glob)
	if err != nil {
		return nil, err
	}
	kept := files[:0]
	for _, rel := range files {
		if view.has(rel) {
			kept = append(kept, rel)
		}
	}
	return kept, nil
}
