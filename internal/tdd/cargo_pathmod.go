package tdd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// pathModDeclRe matches a file-mounting module declaration:
//
//	#[path = "prediction_systems.rs"]
//	mod systems;
//
// Group 1 is the attribute's path, group 3 the name the module is MOUNTED
// under. Attributes may sit between the two (`#[cfg(test)]`, a doc comment's
// `#[doc]` form), and the declaration may be `pub`, `pub(crate)` or bare. Only
// the `;` form is matched: an inline `mod x { … }` mounts no file.
//
// A miss costs nothing but the stem-derived answer this package already gave,
// so the pattern stays deliberately literal rather than trying to be a Rust
// parser. Matching inside a string literal or a comment is the one wrong
// direction, and it cannot mislead either: the resolved path has to name an
// existing file for the mount to be recorded at all, and a commented-out
// declaration that still names its real file describes the same mount the
// live one would.
var pathModDeclRe = regexp.MustCompile(
	`#\[\s*path\s*=\s*"([^"]*)"\s*\]((?:\s*#\[[^\]]*\])*)\s*(?:pub\s*(?:\([^)]*\)\s*)?)?mod\s+([A-Za-z_][A-Za-z0-9_]*)\s*;`)

// maxMountDepth bounds the walk up a chain of #[path] mounts. Real crates
// nest two or three deep; anything past this is malformed input (or a cycle
// the visited set below did not catch), and the stem-derived answer is a
// better response than an unbounded walk.
const maxMountDepth = 16

// pathMount is one recorded mount: the file carrying the declaration, and the
// module name it mounts the target under.
type pathMount struct {
	decl string
	name string
}

// mountKey canonicalises an absolute path for comparison between the two
// sides that must agree: a path composed from a declaration's attribute and
// the path of the file being narrowed. Windows paths compare
// case-insensitively; everywhere else the bytes are the identity.
func mountKey(p string) string {
	p = filepath.Clean(p)
	// Path case-sensitivity is a property of the filesystem, not a style choice:
	// the two sides compared here come from different sources (a walked
	// directory entry and a caller-supplied path), and on Windows they can spell
	// the same file with different case.
	// goos-ok: the comparison this decides IS the platform difference.
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// scanPathMounts reads every .rs file under srcDir and returns, keyed by the
// file each declaration MOUNTS, the declaration that mounts it.
//
// A `#[path]` attribute on a non-inline `mod` resolves relative to the
// directory of the file that carries it — for lib.rs/main.rs/mod.rs and for a
// plain `foo.rs` module file alike — which is what makes a whole-tree scan
// enough: no declaration reaches outside the crate's own source tree to mount
// something, and one keyed by its target is all the walk below needs.
//
// A declaration whose attribute names nothing that exists (or names a
// directory) mounts no file and is dropped: it can neither rename a real
// module nor stand in for one. Two declarations claiming the same file is
// malformed Rust; the lexically first one in WalkDir's order wins, so the
// answer stays deterministic.
//
// Unreadable files and unreadable directories are skipped rather than
// reported: this is an optimisation over the stem-derived path, and a crate
// half of which cannot be read still gets today's answer for the rest.
func scanPathMounts(srcDir string) map[string]pathMount {
	mounts := map[string]pathMount{}
	// absence-ok: a walk error (a missing src dir, an unreadable subtree) means
	// there is nothing more to learn here, and the caller falls back to the
	// stem-derived module path — the answer this package gave before mounts
	// were read at all.
	_ = filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "target" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".rs") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(data), "path") {
			return nil
		}
		for _, m := range pathModDeclRe.FindAllStringSubmatch(string(data), -1) {
			attr := strings.TrimSpace(m[1])
			if attr == "" {
				continue
			}
			target := filepath.Join(filepath.Dir(p), filepath.FromSlash(attr))
			if fi, err := os.Stat(target); err != nil || fi.IsDir() {
				continue
			}
			key := mountKey(target)
			if _, seen := mounts[key]; seen {
				continue
			}
			mounts[key] = pathMount{decl: p, name: m[3]}
		}
		return nil
	})
	return mounts
}

// cargoMountedModulePath returns the module path rel is MOUNTED at when some
// `#[path]` declaration in the crate names it, "" when none does.
//
// This is issue #637: the filter every post-edit run and every `mutants prove`
// uses was derived from the file's own STEM, and a file mounted under another
// name — borld's crates/client/src/prediction_systems.rs, mounted inside
// `prediction` as `mod systems;` — is `prediction::systems`, not
// `prediction_systems`. The stem-derived filter is not a prefix of any test in
// that file, so it selected ZERO tests: an edit that tested nothing, and a
// mutation proof that ran nothing and reported the mutant as a survivor.
//
// The path is composed from the chain of MOUNT names up to the first file no
// declaration mounts, whose own (stem-derived) module path is the base. The
// attribute's directories contribute nothing: `#[path = "generated/tables_
// impl.rs"] mod tables;` in lib.rs is `tables`, full stop.
func cargoMountedModulePath(root, rel string) string {
	srcDir, ok := cargoSrcDirOf(rel)
	if !ok {
		return ""
	}
	mounts := scanPathMounts(filepath.Join(root, filepath.FromSlash(srcDir)))
	if len(mounts) == 0 {
		return ""
	}

	cur := filepath.Join(root, filepath.FromSlash(rel))
	seen := map[string]bool{mountKey(cur): true}
	var chain []string
	for depth := 0; ; depth++ {
		if depth > maxMountDepth {
			return ""
		}
		m, ok := mounts[mountKey(cur)]
		if !ok {
			break
		}
		if seen[mountKey(m.decl)] {
			// A cycle: each file claims to mount the other, so neither has a
			// module path at all. Malformed input is not a reason to hand
			// back half a chain — the stem-derived answer stands.
			return ""
		}
		seen[mountKey(m.decl)] = true
		chain = append([]string{m.name}, chain...)
		cur = m.decl
	}
	if len(chain) == 0 {
		return ""
	}

	declRel, err := filepath.Rel(root, cur)
	if err != nil {
		return ""
	}
	base := cargoModulePath(filepath.ToSlash(declRel))
	if base == "" {
		// Mounted straight from the crate root (lib.rs / main.rs): the mount
		// names are the whole path.
		return strings.Join(chain, "::")
	}
	return base + "::" + strings.Join(chain, "::")
}

// cargoSrcDirOf returns the crate source directory rel lives under — every
// path segment up to and including the first "src" — so the mount scan covers
// exactly the crate that owns the file, workspace member or not.
func cargoSrcDirOf(rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, seg := range parts {
		if seg == "src" && i+1 < len(parts) {
			return strings.Join(parts[:i+1], "/"), true
		}
	}
	return "", false
}

// cargoModuleFilterPath is the module path a src/ file's tests actually live
// under: the #[path] mount when one names the file, and otherwise the
// stem-derived path, unchanged — a crate that mounts nothing by attribute
// (every crate this package saw before #637) gets exactly the answer it
// always got.
func cargoModuleFilterPath(root, rel string) string {
	if mod := cargoMountedModulePath(root, rel); mod != "" {
		return mod
	}
	return cargoModulePath(rel)
}
