package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// releaseDirComponent is the path component this box's own deploy
// convention (deploy/deploy-prod.sh) — and every other CI-deployed aphrollo
// service, which follows the same shape — uses for a build directory the
// very NEXT deploy prunes: OPT_BASE/releases/<ts>-<sha>/, promoted only by
// flipping OPT_BASE/current to point at it. A path carrying this literal
// component is one a shim or hook must never be pinned to by default, no
// matter how runnable it looks the moment it is written: the release it
// names is deleted a few deploys later ("find $RELEASES ... | tail -n +6 |
// xargs rm -rf"), and every gated git/cargo call then degrades to running
// UNGATED with nothing else saying so (the 2026-09-25 incident this file
// fixes). The rule generalizes past this one repo because nothing about the
// check is aphrollo-tools-specific: any deploy that stages a release under a
// directory literally named "releases" trips it.
const releaseDirComponent = "releases"

// underVersionedRelease reports whether path names a file inside a
// versioned-release directory, by literal path component.
func underVersionedRelease(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), string('/')) {
		if part == releaseDirComponent {
			return true
		}
	}
	return false
}

// execPathFn indirects os.Executable so a test can prove the stable-path
// resolution below against a fabricated symlink chain instead of the real
// process's own (never a versioned release) location.
var execPathFn = os.Executable

// stableBinLookPathFn indirects exec.LookPath the same way, so a test can
// fabricate what os.Args[0] would resolve to on PATH without touching this
// process's own PATH.
var stableBinLookPathFn = exec.LookPath

// defaultBinPath is the STABLE absolute path the hooks/shims should invoke,
// so a later CI deploy that prunes an old release can never leave a shim
// pointing at a directory that no longer exists.
//
// os.Executable() resolves EVERY symlink hop, which on a box that deploys
// through a `current` symlink into a releases/<version> directory (see
// releaseDirComponent) lands on the versioned leaf rather than the durable
// entry point. exec.LookPath, by contrast, finds a command on PATH (or
// verifies one given directly) WITHOUT following the symlink it finds any
// further — resolving os.Args[0], the command word the operator (or the
// deploying shell) actually invoked, through it recovers the stable
// spelling, PROVIDED it resolves to the exact same binary os.Executable()
// found, so a stray same-named binary elsewhere on PATH is never
// substituted. When that check finds nothing (a direct invocation of the
// release leaf itself, with no stable alias reachable from argv), the
// `current` sibling this box's own deploy convention promotes a release
// through is tried next. Only when NEITHER survives does this fall back to
// the resolved path itself — the same answer it has always given, and no
// worse than before the fix.
func defaultBinPath() string {
	exe, err := execPathFn()
	if err != nil {
		return "/usr/local/bin/aphrollo"
	}
	if abs, absErr := filepath.Abs(exe); absErr == nil {
		exe = abs
	}
	if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
		exe = resolved
	}
	if !underVersionedRelease(exe) {
		return exe
	}
	if alias := lookPathAlias(exe); alias != "" {
		return alias
	}
	if alias := currentSiblingAlias(exe); alias != "" {
		return alias
	}
	return exe
}

// rawExecutablePath is `aphrollo update`'s OWN --bin default (resolveBinPath,
// selfinstall.go), deliberately NOT defaultBinPath's stable-symlink
// preference. update REPLACES the binary at this path, and its writability
// check right after resolveBinPath (#816, update.go) depends on seeing the
// FULLY symlink-resolved install location — on this box's CI deploy that is
// releases/<ts>-<sha>/, owned by the deploy pipeline's own account, never
// the operator's — to refuse BEFORE wasting a fetch and a build. Defaulting
// update to the stable symlink instead (e.g. /usr/local/bin/aphrollo) would
// check THAT path's writability instead, and on a box where the symlink
// itself sits in an operator-writable directory, update would proceed and
// could overwrite a deploy-managed symlink with a real binary — corrupting
// the very layout the stable path exists to describe. update.go's own
// InstallWritable check is left otherwise untouched.
func rawExecutablePath() string {
	exe, err := execPathFn()
	if err != nil {
		return "/usr/local/bin/aphrollo"
	}
	if abs, absErr := filepath.Abs(exe); absErr == nil {
		return abs
	}
	return exe
}

// lookPathAlias resolves os.Args[0] one hop via stableBinLookPathFn and
// returns it when — and only when — that resolves (by symlink target) to
// the exact same file exe already names. "" otherwise.
func lookPathAlias(exe string) string {
	if len(os.Args) == 0 || os.Args[0] == "" {
		return ""
	}
	resolved, err := stableBinLookPathFn(os.Args[0])
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	// A candidate under a versioned-release directory itself is no
	// improvement over exe — the common shape when argv0 named the release
	// leaf directly (a direct invocation, bypassing every symlink). Leave it
	// for currentSiblingAlias rather than "resolving" to the very path this
	// function exists to avoid.
	if underVersionedRelease(abs) {
		return ""
	}
	if aliasResolvesTo(abs, exe) {
		return abs
	}
	return ""
}

// currentSiblingAlias tries the sibling this box's own deploy convention
// promotes a release through: .../current/<name> beside
// .../releases/<version>/<name>. It walks the path looking for the
// "releases" component so it works regardless of how deep <version> or
// <name> nest, and only returns a candidate that genuinely resolves (by
// symlink target) to exe — never a guessed path that happens not to exist.
// The component AFTER "releases" is the version directory this box's own
// deploy convention prunes; whatever follows THAT is the binary's own name
// (one or more components, e.g. a nested cmd/aphrollo layout), so "releases"
// needs at least two components after it (afterReleases, below) or there is
// no <version>/<name> shape to replace with "current" at all.
func currentSiblingAlias(exe string) string {
	clean := filepath.ToSlash(filepath.Clean(exe))
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		if part != releaseDirComponent {
			continue
		}
		afterReleases := parts[i+1:]
		if len(afterReleases) < 2 {
			continue
		}
		name := afterReleases[1:]
		rebuilt := append(append([]string{}, parts[:i]...), "current")
		rebuilt = append(rebuilt, name...)
		cand := filepath.FromSlash(strings.Join(rebuilt, "/"))
		if aliasResolvesTo(cand, exe) {
			return cand
		}
	}
	return ""
}

// aliasResolvesTo reports whether cand, once its own symlinks are followed,
// names the same file as exe — which the caller has already fully resolved.
func aliasResolvesTo(cand, exe string) bool {
	target, err := filepath.EvalSymlinks(cand)
	if err != nil {
		return false
	}
	return target == exe
}
