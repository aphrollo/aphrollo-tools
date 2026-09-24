package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// InstallCargoShim writes the POSIX half of the cargo-queue shim directory dir
// (e.g. C:/Users/olive/bin/cargo-queue/, alongside wherever aphrollo.exe itself
// lives): an extensionless `cargo` sh script execing `"<bin>" gate cargo` with
// the caller's own args forwarded verbatim. The Windows half is an executable
// COPY of the binary (InstallShimExes), not a batch file — cmd.exe strips `^`
// from an argument and re-splits quoted ones — task A7: any DIRECT `cargo` a
// session runs (not through the hooks/gates) queues behind the SAME
// machine-wide build lock runCargoLocked uses, instead of silently waiting
// on cargo's own build-dir lock with zero visibility. dir and bin are
// SEPARATE parameters (matching InitGitGate's shape, not derived from one
// another) so a caller with a synthetic/test bin string never has this
// write into a path derived from it — dir is always an explicit, real,
// caller-chosen directory.
//
// This does NOT touch the caller's PATH — a session opts in by prepending
// this directory itself. It reports whether either file's content changed
// (a fresh write, or the bin path moved), so init's output can distinguish
// "installed" from "already up to date", matching every other managed-shim
// writer in this package (BuildInstallPlan, InitGitGate).
func InstallCargoShim(dir, bin string) (bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}

	return writeShimIfDifferent(filepath.Join(dir, "cargo"), binShim(bin, "cargo", realToolPath("cargo", dir)), 0o755)
}

// InstallGitShim writes dir's POSIX git-queue shim (task A11): an
// extensionless git sh script execing `"<bin>" gate git` with the caller's own
// args forwarded verbatim -- the git analogue of InstallCargoShim, sharing the
// SAME queue dir so a session that prepends one directory to PATH gets both
// cargo and git queued. Same shape, same idempotency contract.
func InstallGitShim(dir, bin string) (bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return writeShimIfDifferent(filepath.Join(dir, "git"), binShim(bin, "git", realToolPath("git", dir)), 0o755)
}

// writeShimIfDifferent writes content to path only when it differs from
// what's already there (or nothing is there yet), reporting whether it
// wrote. A shared idempotency check so a re-run of `aphrollo tdd init`
// doesn't churn file mtimes for identical content.
func writeShimIfDifferent(path, content string, mode os.FileMode) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
