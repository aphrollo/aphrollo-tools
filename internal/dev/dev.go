// Package dev is the dev-tier control plane: start/stop/restart the
// aphrollo-dev systemd stack and read its status/logs. It replaces the retired
// aphrollo-dev bash wrapper, folding that tooling into this binary.
//
// Privilege model after the bash retired: there is no wrapper script to point
// sudo at, and crucially this general binary carries NO wildcard sudo grant.
// Instead the privileged surface is the narrowest possible —
//
//   - status: needs nothing (systemctl status is readable by any user)
//   - logs:   the dev users are in the systemd-journal group, so journalctl
//     reads the units unprivileged — again no sudo
//   - up/down/restart: a fixed set of exact-match sudoers grants
//     (`systemctl start aphrollo-dev.target`, `systemctl restart
//     aphrollo-dev-rlndx.service`, …) with NO wildcards, so sudo can never be
//     steered onto a unit outside the dev tier
//
// This package builds exactly those argv (unit names are constructed from a
// whitelist, never caller input), so the exact-match sudoers list is the fence.
//
// Unlike the workspace commands (which mutate source/worktrees and default to
// dry-run), these are a service control plane and execute immediately, like
// systemctl itself.
package dev

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target is the dev-tier systemd target that owns the whole stack.
const Target = "aphrollo-dev.target"

var (
	appUnits = []string{"aphrollo-dev-api.service", "aphrollo-dev-rlndx.service"}
	allUnits = []string{"aphrollo-dev-infra.service", "aphrollo-dev-api.service", "aphrollo-dev-rlndx.service"}
	// allowedSvc whitelists the service token; the unit name is ALWAYS
	// constructed as aphrollo-dev-<svc>.service, never caller input.
	allowedSvc = map[string]bool{"api": true, "rlndx": true, "infra": true}
)

func systemctlBin() string  { return envOr("APHROLLO_SYSTEMCTL", "/usr/bin/systemctl") }
func journalctlBin() string { return envOr("APHROLLO_JOURNALCTL", "/usr/bin/journalctl") }

// spacesRoot is the aphrollo spaces dir; the rlndx vite cache hangs off its
// .devclaim/web symlink. Overridable for tests.
func spacesRoot() string { return envOr("APHROLLO_SPACES", "/home/debian/spaces/aphrollo") }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// useSudo reports whether to prefix sudo for the privileged write verbs. Root
// doesn't need it; tests force it off via APHROLLO_DEV_SUDO=0.
func useSudo() bool {
	switch strings.ToLower(os.Getenv("APHROLLO_DEV_SUDO")) {
	case "0", "false", "no":
		return false
	}
	return os.Geteuid() != 0
}

// sudoWrap prefixes sudo onto a privileged systemctl invocation.
func sudoWrap(args ...string) []string {
	if useSudo() {
		return append([]string{"sudo"}, args...)
	}
	return args
}

// ErrServiceRequired and ErrServiceNotAllowed are the two ways a service
// token fails validation, both of which the caller must treat as a USAGE
// error (exit 2) rather than a runtime one (exit 1). They are sentinels
// rather than plain fmt.Errorf so a caller classifies with errors.Is instead
// of matching the rendered message text — a caller that string-matched
// err.Error() would silently reclassify the moment either message here got
// reworded.
var (
	ErrServiceRequired   = errors.New("service required (api|rlndx|infra)")
	ErrServiceNotAllowed = errors.New("service not allowed")
)

// unitFor validates a service token and returns its dev unit name.
func unitFor(svc string) (string, error) {
	if svc == "" {
		return "", ErrServiceRequired
	}
	if !allowedSvc[svc] {
		return "", fmt.Errorf("%w: %s (want api|rlndx|infra)", ErrServiceNotAllowed, svc)
	}
	return "aphrollo-dev-" + svc + ".service", nil
}

// --- argv builders (pure, testable) -----------------------------------------

func startArgv() []string { return sudoWrap(systemctlBin(), "start", Target) }

func stopArgv(all bool) []string {
	if all {
		return sudoWrap(systemctlBin(), "stop", Target)
	}
	return sudoWrap(append([]string{systemctlBin(), "stop"}, appUnits...)...)
}

func restartArgv(svc string) ([]string, error) {
	unit, err := unitFor(svc)
	if err != nil {
		return nil, err
	}
	return sudoWrap(systemctlBin(), "restart", unit), nil
}

// statusArgv reads status unprivileged — no sudo.
func statusArgv() []string {
	return append([]string{systemctlBin(), "--no-pager", "status", Target}, allUnits...)
}

// logsArgv reads the journal unprivileged (the dev users are in
// systemd-journal). An empty svc tails all three units.
func logsArgv(svc string, lines int) ([]string, error) {
	argv := []string{journalctlBin()}
	if svc == "" {
		for _, u := range allUnits {
			argv = append(argv, "-u", u)
		}
	} else {
		unit, err := unitFor(svc)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "-u", unit)
	}
	return append(argv, "--no-pager", "-n", fmt.Sprintf("%d", lines)), nil
}

// rlndxCacheDirs are the stale vite optimizer caches cleared on an rlndx
// restart. They hang off the .devclaim/web symlink that `aphrollo workspace
// claim` repoints, so a bounce clears the CLAIMED tree's cache.
func rlndxCacheDirs() []string {
	// These paths are fed to os.RemoveAll, so refuse to derive them from an
	// unsafe spaces root (empty, relative, or "/"): a misconfigured
	// APHROLLO_SPACES must never let a cache bounce delete from the filesystem
	// root. Skipping is harmless — the cache simply isn't pre-cleared.
	root := filepath.Clean(spacesRoot())
	if !filepath.IsAbs(root) || root == "/" {
		return nil
	}
	base := filepath.Join(root, ".devclaim", "web", "apps", "rlndx")
	return []string{filepath.Join(base, ".svelte-kit"), filepath.Join(base, "node_modules", ".vite")}
}

// --- runners ----------------------------------------------------------------

func runArgv(argv []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// Up starts the whole dev tier.
func Up(stdout, stderr io.Writer) error { return runArgv(startArgv(), stdout, stderr) }

// Down stops the app units; all=true also stops the infra unit (via the target).
func Down(all bool, stdout, stderr io.Writer) error { return runArgv(stopArgv(all), stdout, stderr) }

// Restart bounces one dev unit. For rlndx it first clears the claimed tree's
// stale vite optimizer cache (unprivileged; the caches are user-owned) so the
// restart is a clean reload.
func Restart(svc string, stdout, stderr io.Writer) error {
	argv, err := restartArgv(svc)
	if err != nil {
		return err
	}
	if svc == "rlndx" {
		for _, d := range rlndxCacheDirs() {
			_ = os.RemoveAll(d)
		}
	}
	return runArgv(argv, stdout, stderr)
}

// Status prints dev-tier unit status (unprivileged).
func Status(stdout, stderr io.Writer) error { return runArgv(statusArgv(), stdout, stderr) }

// Logs tails the dev units' journal (unprivileged via the systemd-journal group).
func Logs(svc string, lines int, stdout, stderr io.Writer) error {
	argv, err := logsArgv(svc, lines)
	if err != nil {
		return err
	}
	return runArgv(argv, stdout, stderr)
}
