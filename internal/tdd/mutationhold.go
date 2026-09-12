package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// A hand mutation proof is three steps — break one line, watch the named test
// fail, put the line back byte-identically — and the third step is the one
// with no safe tool behind it. `git checkout -- <file>` restores from the
// INDEX, so mid-lane, where the mutated file also carries uncommitted work of
// its own, it puts back the committed text and takes the operator's unstaged
// edits with it; and for an untracked file it restores nothing at all. Both
// are what issue #650 reported from the field.
//
// A mutation hold is the missing piece: the file's WORKING bytes, copied out
// of the repo before the mutation is written, restorable by content rather
// than by ref. It is scoped to one session (a proof belongs to the session
// running it, not to the box) and expires, because a hold nobody used is
// hours-old content waiting to overwrite a file that has moved on since.

// MutationHoldTTL bounds how long a held working state may be restored. A
// proof is a minutes-long loop; past this the hold is not the state anything
// is still proving anything about, and restoring it would be a silent revert
// rather than a restore.
const MutationHoldTTL = 2 * time.Hour

// maxMutationHolds bounds one session's holds, oldest dropped first, so a
// session that holds and never restores cannot grow the state dir without
// limit.
const maxMutationHolds = 32

// MutationHold is one file's pre-mutation working state: where it came from,
// which blob carries the bytes, what to restore them as, and when it was
// taken. SHA is of the held bytes, checked again after the restore writes
// them back — "restored byte-identically" is the claim this whole path makes,
// so it is verified rather than assumed.
type MutationHold struct {
	Path string    `json:"path"`
	Blob string    `json:"blob"`
	Mode uint32    `json:"mode"`
	Size int64     `json:"size"`
	SHA  string    `json:"sha"`
	At   time.Time `json:"at"`
}

// Age is how long ago the hold was taken, on the same clock the TTL is judged
// against — so a message and the decision behind it never disagree.
func (h MutationHold) Age() time.Duration { return mutationHoldNow().Sub(h.At) }

// errNoMutationSession is what every entry point returns when the environment
// names no session: the holds are per-session by construction, and a hold
// nobody can look up again is worse than a refusal, since the caller would
// then mutate a file believing it could be put back.
var errNoMutationSession = errors.New("no session in the environment (CLAUDE_SESSION_ID): a mutation hold is scoped to the session that takes it")

// mutationHoldClock lets a test observe the TTL without a real wait.
var mutationHoldClock atomic.Pointer[func() time.Time]

func mutationHoldNow() time.Time {
	if p := mutationHoldClock.Load(); p != nil {
		return (*p)()
	}
	return time.Now()
}

// SetMutationHoldClockForTest overrides the clock the holds are timed on, for
// the duration of a test. Exported because internal/cli drives the holds
// through the git shim, from another package.
func SetMutationHoldClockForTest(clock func() time.Time) (restore func()) {
	prev := mutationHoldClock.Swap(&clock)
	return func() { mutationHoldClock.Store(prev) }
}

// HoldMutation copies path's current bytes into this session's hold store and
// returns the hold. Taking a second hold of the same file REPLACES the first:
// a proof that restored and went round again holds the state it is about to
// mutate, never the one two rounds back.
func HoldMutation(path string) (MutationHold, error) {
	dir, err := mutationHoldDir()
	if err != nil {
		return MutationHold{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return MutationHold{}, err
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return MutationHold{}, fmt.Errorf("cannot read %s to hold its working state: %w", abs, err)
	}
	mode := os.FileMode(0o644)
	if fi, statErr := os.Stat(abs); statErr == nil {
		mode = fi.Mode().Perm()
	}
	sum := sha256.Sum256(body)
	hold := MutationHold{
		Path: abs,
		Blob: blobNameFor(abs),
		Mode: uint32(mode),
		Size: int64(len(body)),
		SHA:  hex.EncodeToString(sum[:]),
		At:   mutationHoldNow().UTC(),
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return MutationHold{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, hold.Blob), body, 0o600); err != nil {
		return MutationHold{}, err
	}
	holds := append(withoutHoldFor(readHolds(dir), abs), hold)
	if err := writeHolds(dir, holds); err != nil {
		return MutationHold{}, err
	}
	return hold, nil
}

// MutationHoldFor answers the live hold for path, if this session took one
// and it has not expired. An expired hold answers false rather than being
// silently honoured — and is still RETURNED, so the caller's refusal can say
// how stale it is instead of claiming there was never a hold at all.
func MutationHoldFor(path string) (MutationHold, bool) {
	dir, err := mutationHoldDir()
	if err != nil {
		// absence-ok: no session and no state dir are the two ways there can
		// be no hold at all, which is what this lookup reports; the callers
		// that need the reason (HoldMutation, RestoreMutationHold) ask
		// mutationHoldDir themselves and propagate its error.
		return MutationHold{}, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		// absence-ok: a path that cannot be made absolute matches no held
		// path, and this lookup's caller refuses the restore either way.
		return MutationHold{}, false
	}
	for _, h := range readHolds(dir) {
		if samePath(h.Path, abs) {
			return h, h.Age() <= MutationHoldTTL
		}
	}
	return MutationHold{}, false
}

// RestoreMutationHold writes the held bytes back over path and verifies the
// result matches the hold's own SHA. The hold is KEPT: a proof commonly runs
// the loop more than once, and re-taking a hold of a file already restored to
// the same bytes would be the same hold anyway.
//
// It never consults git, which is the point — an untracked file, a file with
// unstaged work, and a file the proof deleted outright all restore the same
// way, from content.
func RestoreMutationHold(path string) (MutationHold, error) {
	dir, err := mutationHoldDir()
	if err != nil {
		return MutationHold{}, err
	}
	hold, ok := MutationHoldFor(path)
	if !ok {
		if hold.Path != "" {
			return hold, fmt.Errorf("the mutation hold for %s was taken %s ago, past the %s a proof may hold a file",
				hold.Path, hold.Age().Round(time.Second), MutationHoldTTL)
		}
		return MutationHold{}, fmt.Errorf("no mutation hold for %s in this session", path)
	}
	body, err := os.ReadFile(filepath.Join(dir, hold.Blob))
	if err != nil {
		return hold, fmt.Errorf("the held working state of %s is unreadable: %w", hold.Path, err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != hold.SHA {
		return hold, fmt.Errorf("the held working state of %s does not match its own checksum; nothing was written", hold.Path)
	}
	if err := os.WriteFile(hold.Path, body, os.FileMode(hold.Mode)); err != nil {
		return hold, err
	}
	written, err := os.ReadFile(hold.Path)
	if err != nil {
		return hold, err
	}
	back := sha256.Sum256(written)
	if hex.EncodeToString(back[:]) != hold.SHA {
		return hold, fmt.Errorf("%s does not match the held working state after the restore", hold.Path)
	}
	return hold, nil
}

// mutationHoldDir is this session's hold directory, or the reason there is
// none.
func mutationHoldDir() (string, error) {
	session := SessionID()
	if session == "" {
		return "", errNoMutationSession
	}
	base := stateDir()
	if base == "" {
		return "", errors.New("no gate state directory (CLAUDE_CONFIG_DIR or a home directory) to hold a working state in")
	}
	return filepath.Join(base, "mutation-holds", sanitizeHoldName(session)), nil
}

// sanitizeHoldName keeps a session id to the characters a directory name may
// safely carry on every platform this runs on.
func sanitizeHoldName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// blobNameFor names the blob for an absolute path: the path's own hash, so
// two files sharing a base name in different trees never collide and no path
// component ever reaches the filesystem as a name.
func blobNameFor(abs string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return hex.EncodeToString(sum[:])[:32] + ".blob"
}

// readHolds reads the index, oldest first. A missing or unreadable index
// reads as no holds: the caller's own refusal then says there is no hold for
// this file, which is the true answer either way.
func readHolds(dir string) []MutationHold {
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil // absence-ok: no index file means this session has held nothing, which is the answer rather than an error
	}
	var holds []MutationHold
	if err := json.Unmarshal(data, &holds); err != nil {
		return nil // absence-ok: an index this binary cannot parse holds nothing it can restore from, and every caller refuses on "no hold"
	}
	sort.Slice(holds, func(i, j int) bool { return holds[i].At.Before(holds[j].At) })
	return holds
}

// writeHolds replaces the index, dropping the oldest past the bound (and the
// blobs with them, so the store does not keep bytes nothing can reach).
func writeHolds(dir string, holds []MutationHold) error {
	sort.Slice(holds, func(i, j int) bool { return holds[i].At.Before(holds[j].At) })
	for len(holds) > maxMutationHolds {
		_ = os.Remove(filepath.Join(dir, holds[0].Blob))
		holds = holds[1:]
	}
	data, err := json.Marshal(holds)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0o600)
}

// withoutHoldFor drops any existing hold for abs, so a re-hold replaces
// rather than shadows.
func withoutHoldFor(holds []MutationHold, abs string) []MutationHold {
	var out []MutationHold
	for _, h := range holds {
		if samePath(h.Path, abs) {
			continue
		}
		out = append(out, h)
	}
	return out
}
