package tdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// A binary that never rebuilds itself drifts from origin/main silently: the
// fix landed, merged, and the box just keeps running the old one until
// someone happens to `git pull` the tool repo and rebuild by hand. The
// session is the one place that notices, so it says so once an hour rather
// than never.

// binaryBehindCacheTTL is how long one `git ls-remote` answers for. An hour:
// long enough that a day of sessions in ANY repo on the box costs a handful
// of calls (this line is about the binary, not the repo the hook is running
// in), short enough that a same-day merge shows up the same day.
const binaryBehindCacheTTL = time.Hour

// binaryBehindTimeout bounds the one `git ls-remote` call this line costs. A
// session start that hangs on the network is worse than one that occasionally
// says nothing about a stale binary.
const binaryBehindTimeout = 2 * time.Second

// binaryBehindRemote is the one repo this notice ever asks about.
const binaryBehindRemote = "https://github.com/aphrollo/aphrollo-tools"

// binaryBehindCacheFile is the state-dir cache, private per CLAUDE_CONFIG_DIR
// (so a test pointing it at a temp dir never sees another session's answer).
const binaryBehindCacheFile = "binary-behind.json"

// binaryBehindFailureBackoff is how long a FAILED lookup answers "" for
// without touching the network again: a network outage otherwise re-pays the
// full 2 s budget on every single session start until it clears, which is the
// same hang this feature exists to prevent. Shorter than the hour success TTL
// because a real merge landing during an outage should show up soon after the
// network recovers, not up to an hour late.
const binaryBehindFailureBackoff = 10 * time.Minute

// lsRemoteFn asks the remote for the sha at the tip of main. A var so a test
// can replace the network call with a seam; the production body wires
// runLsRemote to the git this package already resolves (gitBinary skips the
// queue shim, which would otherwise re-enter aphrollo instead of reaching
// git).
var lsRemoteFn = func(ctx context.Context) (string, error) {
	return runLsRemote(ctx, gitBinary())
}

// runLsRemote runs `git ls-remote --heads <remote> main` under ctx (a 2 s
// budget in production, see BinaryBehindLine) and returns the 40-hex sha at
// the front of the one line it prints (`<sha>\trefs/heads/main`). gitProgram
// is a parameter (rather than a bare "git") so a test can point it at a stub
// that reproduces the hang this guards against.
//
// `git ls-remote https://...` spawns a git-remote-https helper that inherits
// the stdout pipe cmd.Output() wires up. The default Cancel that
// exec.CommandContext installs kills only the git pid, not that helper — so
// on a timeout the helper can keep the pipe open and Wait() blocks past ctx's
// deadline. cmd.Cancel reaches the whole tree (the same proc.KillTree a
// deferred build phase already uses) and WaitDelay bounds how long Wait() waits for
// I/O to drain after that, so the call returns within its budget even when
// the helper never exits on its own.
func runLsRemote(ctx context.Context, gitProgram string) (string, error) {
	cmd := exec.CommandContext(ctx, gitProgram, "ls-remote", "--heads", binaryBehindRemote, "main")
	cmd.SysProcAttr = suiteAttrs()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return proc.KillTree(cmd.Process.Pid)
	}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("git ls-remote: no output")
	}
	return fields[0], nil
}

// binaryBehindStage and its two failure verdicts are the tokens this check
// writes to gate.log when it gives up, so a permanently broken `ls-remote`
// leaves a trail in the same ledger `gate stats` reads instead of vanishing
// into the same silence as a healthy "already current" answer. A timeout and
// a plain command failure are different problems (a hung remote vs. a
// rejected one) and must not collapse into one token.
const (
	binaryBehindStage            = "binary-behind"
	binaryBehindCmd              = "git ls-remote"
	binaryBehindStanddownTimeout = "standdown-timeout"
	binaryBehindStanddownFailed  = "standdown-failed"
	binaryBehindRecovered        = "recovered"
)

// binaryBehindCache is the remembered answer to "what is the tip of main".
// FailedAt is set (and CheckedAt/Head left as whatever the last SUCCESSFUL
// lookup produced, possibly zero/"" if there never was one) when the most
// recent lookup errored, so a later success can still overwrite it; a
// successful write always omits FailedAt, clearing the backoff. FailKind is
// the reason last RECORDED in gate.log ("" when the last recorded outcome
// was a success): comparing against it is what lets a failure that persists
// across many session starts log its standdown exactly once, at the
// transition, rather than every time the backoff clears.
type binaryBehindCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Head      string    `json:"head"`
	FailedAt  time.Time `json:"failed_at,omitzero"`
	FailKind  string    `json:"fail_kind,omitempty"`
}

// BinaryBehindLine is the session-start line naming a binary built from an
// older commit than origin/main. "" when the binary was never stamped (a
// `go build` run by hand), the cached or freshly fetched head matches the
// stamp, or the fetch failed or ran past its budget — a network hiccup is not
// information a session can act on, so it stays silent rather than crying
// wolf. now is passed in so the hourly cache window is a decision the caller
// can test rather than a stopwatch reading.
func BinaryBehindLine(now time.Time) string {
	commit, _, stamped := buildinfo.Stamp()
	if !stamped {
		return ""
	}
	path := gcStatePath(binaryBehindCacheFile)
	cache, hasCache := readBinaryBehindCache(path)
	head, ok := freshBinaryBehindHead(cache, hasCache, now)
	if !ok {
		if hasCache && recentlyFailedBinaryBehindLookup(cache, now) {
			return ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), binaryBehindTimeout)
		defer cancel()
		h, err := lsRemoteFn(ctx)
		if err != nil || h == "" {
			kind := binaryBehindStanddownFailed
			if ctx.Err() != nil {
				kind = binaryBehindStanddownTimeout
			}
			recordBinaryBehindFailure(path, cache, now, kind)
			return ""
		}
		if cache.FailKind != "" {
			// The transition OUT of failure is as much a state change as the
			// one into it — logged once, here, never on a plain success that
			// never failed.
			appendGateLog(binaryBehindStage, "", binaryBehindCmd, binaryBehindRecovered, 0)
		}
		head = h
		if path != "" {
			if data, err := json.Marshal(binaryBehindCache{CheckedAt: now, Head: head}); err == nil {
				_ = writeFileAtomic(path, data)
			}
		}
	}
	if head == commit {
		return ""
	}
	return fmt.Sprintf("aphrollo binary is behind origin/main (built at %s, origin at %s): run aphrollo update",
		shortSHA(commit), shortSHA(head))
}

// readBinaryBehindCache reads and parses the cache file, reporting whether
// one was found at all — a missing or unparsable cache is "no cache", not an
// error, since the very first session on a box never wrote one.
func readBinaryBehindCache(path string) (binaryBehindCache, bool) {
	if path == "" {
		return binaryBehindCache{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return binaryBehindCache{}, false
	}
	var c binaryBehindCache
	if json.Unmarshal(data, &c) != nil {
		return binaryBehindCache{}, false
	}
	return c, true
}

// freshBinaryBehindHead reports the cached head when it is still within the
// success TTL. A cache stamped in the future (a clock that moved) counts as
// stale rather than served until it "expires".
func freshBinaryBehindHead(c binaryBehindCache, has bool, now time.Time) (string, bool) {
	if !has || c.Head == "" {
		return "", false
	}
	age := now.Sub(c.CheckedAt)
	if age < 0 || age >= binaryBehindCacheTTL {
		return "", false
	}
	return c.Head, true
}

// recentlyFailedBinaryBehindLookup reports whether the last lookup errored
// within the failure backoff window, so an outage re-pays the network budget
// once every 10 minutes rather than once every session start.
func recentlyFailedBinaryBehindLookup(c binaryBehindCache, now time.Time) bool {
	if c.FailedAt.IsZero() {
		return false
	}
	age := now.Sub(c.FailedAt)
	return age >= 0 && age < binaryBehindFailureBackoff
}

// recordBinaryBehindFailure remembers a failed lookup without disturbing
// whatever CheckedAt/Head the last SUCCESSFUL lookup left behind, so a retry
// once the backoff clears still has the old answer to fall back on if it
// fails again immediately. It also logs kind ONCE per transition — only when
// it differs from the reason last recorded — so a repeat of the SAME failure
// across many session starts stays silent in the ledger.
//
// The log call is written before the path=="" cache-write bailout, but that
// ordering buys NOTHING extra: path comes from gcStatePath, which derives
// dir from the same stateDir() and runs the same os.MkdirAll(dir, 0o700)
// appendGateLog (state.go) independently repeats before it will write a
// line. Every real cause of path=="" — no CLAUDE_CONFIG_DIR and no resolvable
// home, or a dir this account cannot create — reproduces inside
// appendGateLog too, so the standdown token is silently lost in exactly the
// case this comment used to claim it was protected. There is nowhere else on
// this box to write it; a state dir this unwritable is a separate, larger
// problem the doctor's own "lock dirs writable" check exists to catch.
func recordBinaryBehindFailure(path string, prev binaryBehindCache, now time.Time, kind string) {
	if prev.FailKind != kind {
		appendGateLog(binaryBehindStage, "", binaryBehindCmd, kind, 0)
	}
	prev.FailedAt = now
	prev.FailKind = kind
	if path == "" {
		return
	}
	if data, err := json.Marshal(prev); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

// shortSHA truncates to the 7-char form `aphrollo version` already prints,
// unchanged for a shorter string.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
