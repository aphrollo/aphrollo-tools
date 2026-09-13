package tdd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Issue #651's second half. A stale `forge_powertrain` fingerprint made the
// post-edit hook report a RED three times running, citing `no wear in the
// root` for a module that was right there in the crate, while
// `cargo check -p server --tests` in the same worktree was clean. Two lanes
// in sibling worktrees were building that crate, which is how the artifact
// went stale in the first place.
//
// The heuristic is deliberately NARROW, and the narrowness is the design:
// a wrong guess here teaches people to delete build state whenever a real
// error confuses them, which is a worse failure than the one being fixed.
// So the hint needs BOTH halves to hold —
//
//  1. a diagnostic names a symbol that a cheap textual scan of the crate's
//     OWN sources does find ("the compiler says X does not exist, but X is
//     right there"), and
//  2. that crate's target dir is shared with, or concurrently used by,
//     another checkout, which is the condition that actually makes a
//     fingerprint go stale.
//
// When either fails it prints NOTHING: an ordinary compile error is the
// overwhelmingly common case and deserves silence. And when both hold it
// still only SUGGESTS — it names the fingerprint and the command to remove
// it, never removes anything itself, and never touches the verdict. The RED
// stays a RED; the hint is about what might explain it.

// staleArtifactSymbolRes are the rustc diagnostics that assert a symbol does
// not exist. `file not found for module` (E0583) is deliberately absent: its
// `mod x;` declaration is exactly what the scan below would find, so rustc
// having already read it makes a match there a guaranteed false positive —
// that error means the FILE is missing and has a real fix.
var staleArtifactSymbolRes = []*regexp.Regexp{
	regexp.MustCompile("no `([^`]+)` in (?:the root|`[^`]+`)"),
	regexp.MustCompile("use of undeclared (?:crate or module|type) `([^`]+)`"),
	regexp.MustCompile("cannot find (?:function|value|struct|type|trait|macro|module|method|attribute|item) `([^`]+)`"),
	regexp.MustCompile("unresolved import `([^`]+)`"),
	regexp.MustCompile("no (?:method|function|variant|associated item|variant or associated item) named `([^`]+)`"),
}

// diagLocationRe reads the file a rustc diagnostic points at. The path is
// relative to the directory cargo ran in, and carries a :line:col suffix.
var diagLocationRe = regexp.MustCompile(`^\s*-->\s+(\S+)`)

// identRe bounds what counts as a symbol worth scanning for: a plain Rust
// identifier. A capture carrying spaces, generics or punctuation is left
// alone rather than guessed at.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// missingSymbol is one "this does not exist" claim: the identifier, and the
// source file the diagnostic pointed at.
type missingSymbol struct {
	name string
	file string
}

// staleArtifactHint is the extra advisory a RED summary carries when the
// failure has the shape of a stale build artifact, "" — silence — in every
// other case. dir is the directory the build ran in, which is what rustc's
// own relative diagnostic paths resolve against.
func staleArtifactHint(dir, output string) string {
	syms := diagnosedMissingSymbols(dir, output)
	if len(syms) == 0 {
		return ""
	}
	share, shared := targetDirSharing(dir)
	if !shared {
		return ""
	}
	for _, s := range syms {
		crateDir, pkg, ok := crateOwning(s.file)
		if !ok {
			continue
		}
		declPath, declLine, found := declarationSite(crateDir, s.name)
		if !found {
			continue
		}
		return staleArtifactLines(s.name, pkg, relativeTo(dir, declPath), declLine, share)
	}
	return ""
}

// staleArtifactLines is the wording, in one place: what the compiler claims,
// what the source says, why the artifacts are suspect, and the exact removal
// to run BY HAND. It says plainly that nothing was deleted and that the
// failure stands, because a hint that reads like a verdict is how a session
// learns to reach for `rm -rf` ahead of reading the error.
func staleArtifactLines(symbol, pkg, declPath string, declLine int, share targetDirShare) string {
	glob := filepath.ToSlash(filepath.Join(share.dir, "debug", ".fingerprint", pkg+"-*"))
	return fmt.Sprintf(
		"possible stale artifact: the build says `%s` does not exist, but crate %s declares it at %s:%d, and %s.\n"+
			"nothing was deleted and the failure above stands — if this error makes no sense here, remove that crate's fingerprint by hand and re-run: rm -rf %q",
		symbol, pkg, declPath, declLine, share.reason, glob)
}

// diagnosedMissingSymbols pairs every "does not exist" claim in output with
// the source file its diagnostic points at. rustc writes the claim on the
// `error[...]` header line for some codes and on the caret line for others,
// so each claim takes the NEAREST `-->` location — the following one for a
// header, the preceding one for a caret. A claim with no resolvable location
// is dropped: without a file there is no crate to scan, and a guess about
// which crate is exactly the false positive this is built to avoid.
func diagnosedMissingSymbols(dir, output string) []missingSymbol {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	type located struct {
		line int
		path string
	}
	var locs []located
	for i, line := range lines {
		if m := diagLocationRe.FindStringSubmatch(line); m != nil {
			locs = append(locs, located{line: i, path: m[1]})
		}
	}
	if len(locs) == 0 {
		return nil
	}
	var out []missingSymbol
	seen := map[string]bool{}
	for i, line := range lines {
		name, ok := claimedMissingName(line)
		if !ok || seen[name] {
			continue
		}
		nearest := locs[0]
		for _, l := range locs[1:] {
			if abs(l.line-i) < abs(nearest.line-i) {
				nearest = l
			}
		}
		file, ok := resolveDiagPath(dir, nearest.path)
		if !ok {
			continue
		}
		seen[name] = true
		out = append(out, missingSymbol{name: name, file: file})
	}
	return out
}

// claimedMissingName reads the identifier one diagnostic line says is absent.
// A qualified path (`crate::wear`) answers its LAST segment, which is the
// segment that failed to resolve; anything that is not a plain identifier
// answers false rather than being guessed at.
func claimedMissingName(line string) (string, bool) {
	for _, re := range staleArtifactSymbolRes {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if i := strings.LastIndex(name, "::"); i >= 0 {
			name = name[i+2:]
		}
		if identRe.MatchString(name) {
			return name, true
		}
	}
	return "", false
}

// resolveDiagPath turns a diagnostic's `path:line:col` into an existing file
// on disk, resolved against the directory the build ran in. ok=false for a
// path that does not resolve — the hint stays silent rather than scanning a
// tree it guessed at.
func resolveDiagPath(dir, raw string) (string, bool) {
	path := raw
	for range 2 {
		if i := strings.LastIndex(path, ":"); i > 1 {
			if _, err := os.Stat(path); err == nil {
				break
			}
			path = path[:i]
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, filepath.FromSlash(path))
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", false // absence-ok: no file, no crate to scan
	}
	return path, true
}

// crateOwning walks up from a source file to the nearest Cargo.toml that
// declares a [package], answering that crate's directory and name — the
// crate whose OWN sources the scan is allowed to look at, and whose
// fingerprint the hint would name. ok=false when no package owns the file.
func crateOwning(file string) (string, string, bool) {
	dir := filepath.Dir(file)
	for {
		if name := cargoPackageName(filepath.Join(dir, "Cargo.toml")); name != "" {
			return dir, name, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

// staleArtifactFileCap and staleArtifactByteCap bound the scan. It is a
// textual sweep of one crate on a path that already failed a build, not a
// parser and not a whole-workspace index: a crate too large to sweep under
// these bounds answers "not found", and a false negative here costs nothing
// at all.
const (
	staleArtifactFileCap = 500
	staleArtifactByteCap = 512 * 1024
)

// declarationSite finds where a crate's own sources DECLARE name, answering
// the file and 1-based line. It matches declaration forms only (`mod`, `fn`,
// `struct`, `enum`, `trait`, `type`, `union`, `const`, `static`,
// `macro_rules!`) — searching for the bare token would match the use site
// that produced the diagnostic, which would make every error its own
// evidence. Lexical walk order makes the answer deterministic; `target` and
// dot-directories are skipped, so generated artifacts never stand in for
// source.
func declarationSite(crateDir, name string) (string, int, bool) {
	re := declarationRe(name)
	foundPath, foundLine, scanned := "", 0, 0
	_ = filepath.WalkDir(crateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // absence-ok: an unreadable entry is one less candidate
		}
		if d.IsDir() {
			// The walk's own root is the one directory whose name says
			// nothing about it (a crate can live under a dot-directory, and
			// skipping it would end the scan before it started). insideDir
			// answers that on both hosts: crateDir is inside path only for
			// the root of this walk, the only ancestor it ever visits.
			if insideDir(path, crateDir) {
				return nil
			}
			base := d.Name()
			if base == "target" || strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".rs" {
			return nil
		}
		if scanned >= staleArtifactFileCap {
			return fs.SkipAll
		}
		scanned++
		if info, err := d.Info(); err == nil && info.Size() > staleArtifactByteCap {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // absence-ok
		}
		loc := re.FindIndex(data)
		if loc == nil {
			return nil
		}
		foundPath = path
		foundLine = 1 + strings.Count(string(data[:loc[0]]), "\n")
		return fs.SkipAll
	})
	return foundPath, foundLine, foundPath != ""
}

// declarationRe builds the declaration matcher for one identifier.
func declarationRe(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`(?m)^[ \t]*(?:macro_rules![ \t]*` + q +
		`\b|(?:pub[ \t]*(?:\([^)]*\))?[ \t]+)?(?:(?:async|const|unsafe|default|extern)[ \t]+|"[^"]*"[ \t]+)*` +
		`(?:mod|fn|struct|enum|trait|type|union|const|static)[ \t]+` + q + `\b)`)
}

// targetDirShare is the second condition's finding: which target dir is
// suspect, and the clause saying why it is.
type targetDirShare struct {
	dir    string
	reason string
}

// targetDirSharing decides whether dir's build artifacts are exposed to
// another checkout at all — the only condition under which a fingerprint
// goes stale the way issue #651 describes. Two ways it can be true, both
// read off machinery this package already owns (buildslots.go):
//
//   - the resolved target dir lies OUTSIDE this checkout (CARGO_TARGET_DIR,
//     or a build.target-dir in some ancestor or the user's cargo config), so
//     every checkout resolving to it writes the same artifacts; or
//   - another checkout holds a build slot on it right now, with a live pid —
//     the same liveness rule SnapshotBuildSlots applies, so a corpse owner
//     file left by a killed build cannot keep this firing forever.
//
// ok=false — a target dir private to this checkout and busy with nobody —
// buys silence.
func targetDirSharing(dir string) (targetDirShare, bool) {
	target := ResolveCargoTargetDir(dir)
	ws := cargoWorkspaceRoot(dir)
	if !insideDir(ws, target) {
		return targetDirShare{
			dir:    target,
			reason: fmt.Sprintf("its target dir %s sits outside this checkout, shared with every other checkout that resolves to it", target),
		}, true
	}
	if o, ok := ReadBuildSlotOwner(target); ok && pidRunningFn(o.PID) && !insideDir(ws, o.Cwd) {
		return targetDirShare{
			dir:    target,
			reason: fmt.Sprintf("another checkout is building into its target dir %s right now (%q in %s)", target, o.Cmd, o.Cwd),
		}, true
	}
	return targetDirShare{}, false
}

// relativeTo renders path for a human reading it beside the command they
// ran: repo-relative with forward slashes when it can be, verbatim when it
// cannot.
func relativeTo(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// abs is the integer absolute value the nearest-location search needs.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
