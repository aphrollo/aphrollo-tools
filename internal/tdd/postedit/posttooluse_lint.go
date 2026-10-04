package postedit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The commit gate refuses a commit over a golangci-lint finding, and the finding
// is knowable while the edit is made (issue #1006). The run the edit hook
// requests carries that lint (lintrun.go): it runs once, after the run's tests,
// under the lint lock, and its findings ride the run's line as guidance. This
// file holds what that lint shares with the rest of the hook: whether the linter
// is installed, whether the box is too loaded for one, the backoff after one
// that ran past its budget, and how a finding is read and named.
const (
	// lintEditBackoff is how long a lint that ran past its budget keeps the next
	// one off, so a cold cache costs one timeout, not one per edit.
	lintEditBackoff = 10 * time.Minute
	// lintEditShown is how many findings the note names before it counts the
	// rest.
	lintEditShown = 5
	// lintEditLoadPerCore is the runnable load per core at which the box is
	// too busy to add a lint.
	lintEditLoadPerCore = 2
)

// lintEditFinding reads one finding line, "path:line:col: message (linter)".
var lintEditFinding = regexp.MustCompile(`^(\S.*?\.go):\d+:\d+: .+$`)

// The seams of the lint: whether the linter is installed and the box's load,
// so a test states each without the box's.
var (
	lintEditLook = golangciLintOnPath
	lintEditLoad = readLoadAvg
)

// golangciLintOnPath reports whether golangci-lint is installed.
func golangciLintOnPath() bool {
	_, err := exec.LookPath("golangci-lint")
	return err == nil
}

// namedFindings names the first lintEditShown findings and counts the rest.
func namedFindings(findings []string) string {
	more := ""
	if len(findings) > lintEditShown {
		more = fmt.Sprintf(" and %d more", len(findings)-lintEditShown)
		findings = findings[:lintEditShown]
	}
	return strings.Join(findings, "; ") + more
}

// lintBoxLoaded reports whether the box's runnable load is at or past
// lintEditLoadPerCore per core. An unreadable load is not loaded.
func lintBoxLoaded() bool {
	load, cores, ok := lintEditLoad()
	return ok && load >= lintEditLoadPerCore*float64(cores)
}

// readLoadAvg is the box's one-minute load average and core count, from
// /proc/loadavg; not ok where there is none.
func readLoadAvg() (load float64, cores int, ok bool) {
	return loadFromProc(func() ([]byte, error) { return os.ReadFile("/proc/loadavg") })
}

// loadFromProc reads the one-minute load average from the text read returns.
func loadFromProc(read func() ([]byte, error)) (load float64, cores int, ok bool) {
	data, err := read()
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, 0, false
	}
	load, err = strconv.ParseFloat(fields[0], 64)
	return load, runtime.NumCPU(), err == nil
}

// lintBackoffMarker is the file whose mtime says when a lint in root last
// ran past its budget.
func lintBackoffMarker(root string) string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "lint-backoff-"+hex.EncodeToString(sum[:6]))
}

// lintBackedOff reports whether a lint in root ran past its budget within
// lintEditBackoff.
func lintBackedOff(root string) bool {
	marker := lintBackoffMarker(root)
	if marker == "" {
		return false
	}
	info, err := os.Stat(marker)
	return err == nil && withinBackoff(time.Since(info.ModTime()))
}

// withinBackoff reports whether a lint that ran past its budget age ago still
// holds the next one off.
func withinBackoff(age time.Duration) bool {
	return age < lintEditBackoff
}

// markLintBackoff records that a lint in root ran past its budget just now.
func markLintBackoff(root string) {
	if marker := lintBackoffMarker(root); marker != "" {
		// A backoff that cannot be recorded costs the next edit one more try.
		_ = os.MkdirAll(filepath.Dir(marker), 0o700)
		_ = os.WriteFile(marker, nil, 0o600)
	}
}
