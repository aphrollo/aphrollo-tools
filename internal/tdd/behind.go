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

// lsRemoteFn asks the remote for the sha at the tip of main. A var so a test
// can replace the network call with a seam; the production body is
// runLsRemote below.
var lsRemoteFn = runLsRemote

// runLsRemote runs `git ls-remote --heads <remote> main` under ctx (a 2 s
// budget in production, see BinaryBehindLine) and returns the 40-hex sha at
// the front of the one line it prints (`<sha>\trefs/heads/main`).
func runLsRemote(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--heads", binaryBehindRemote, "main").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("git ls-remote: no output")
	}
	return fields[0], nil
}

// binaryBehindCache is the remembered answer to "what is the tip of main".
type binaryBehindCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Head      string    `json:"head"`
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
	head, ok := cachedBinaryBehindHead(path, now)
	if !ok {
		ctx, cancel := context.WithTimeout(context.Background(), binaryBehindTimeout)
		defer cancel()
		h, err := lsRemoteFn(ctx)
		if err != nil || h == "" {
			return ""
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

// cachedBinaryBehindHead reads the cache and reports whether it is still
// within the TTL. A cache stamped in the future (a clock that moved) counts
// as stale rather than served until it "expires".
func cachedBinaryBehindHead(path string, now time.Time) (string, bool) {
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var c binaryBehindCache
	if json.Unmarshal(data, &c) != nil {
		return "", false
	}
	age := now.Sub(c.CheckedAt)
	if age < 0 || age >= binaryBehindCacheTTL {
		return "", false
	}
	return c.Head, true
}

// shortSHA truncates to the 7-char form `aphrollo version` already prints,
// unchanged for a shorter string.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
