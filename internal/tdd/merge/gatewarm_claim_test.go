package merge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Two takers meet one stale claim: exactly one wins. The second arrives while
// the first has judged the claim stale and not yet replaced it; a takeover that
// removes whatever it finds would take the first one's live claim away.
func TestWarmClaim_TwoTakersOnOneStaleClaimExactlyOneWins(t *testing.T) {
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(claim, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wins := 0
	inner := false
	warmClaimJudged = func() {
		if inner {
			return
		}
		inner = true
		if takeWarmClaim(claim) {
			wins++
		}
	}
	t.Cleanup(func() { warmClaimJudged = func() {} })
	if takeWarmClaim(claim) {
		wins++
	}
	if wins != 1 {
		t.Fatalf("%d takers won one stale claim, want exactly 1", wins)
	}
}

func warmWriteClaim(t *testing.T, body string) string {
	t.Helper()
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(claim, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return claim
}

// A pid the system handed out again after the claimant died names a different
// process: the claim carries the identity it was stamped with, and a live pid
// with another identity is stale. The same identity is still held.
func TestWarmClaim_ReusedPidWithAnotherIdentityIsStale(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	body := "pid=" + pid + "\nstarted=" + time.Now().UTC().Format(time.RFC3339Nano) + "\nid=boot:111\n"
	old := warmIdentityFn
	t.Cleanup(func() { warmIdentityFn = old })

	warmIdentityFn = func(int) (string, bool) { return "boot:111", true }
	if takeWarmClaim(warmWriteClaim(t, body)) {
		t.Fatal("a claim whose pid and identity both match a live process was taken")
	}
	warmIdentityFn = func(int) (string, bool) { return "boot:222", true }
	if !takeWarmClaim(warmWriteClaim(t, body)) {
		t.Fatal("a claim whose pid now belongs to another process held the checkout")
	}
}

// Where no identity can be read, a live pid keeps a claim only for a bounded
// time: a claim older than the bound is stale, a recent one is held.
func TestWarmClaim_LivePidWithAnOldClaimIsStale(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	at := func(ago time.Duration) string {
		return "pid=" + pid + "\nstarted=" + time.Now().Add(-ago).UTC().Format(time.RFC3339Nano) + "\n"
	}
	if takeWarmClaim(warmWriteClaim(t, at(time.Minute))) {
		t.Fatal("a recent claim of a live pid was taken")
	}
	if !takeWarmClaim(warmWriteClaim(t, at(warmClaimMaxAge+time.Hour))) {
		t.Fatal("a claim older than the bound still held the checkout")
	}
}

// The claim a taker writes carries what the stale checks read: its pid, when it
// took the claim, and its process identity.
func TestWarmClaim_TheClaimWrittenCarriesPidStartAndIdentity(t *testing.T) {
	old := warmIdentityFn
	t.Cleanup(func() { warmIdentityFn = old })
	warmIdentityFn = func(int) (string, bool) { return "boot:777", true }
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if !takeWarmClaim(claim) {
		t.Fatal("a free claim was not taken")
	}
	b, err := os.ReadFile(claim)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "pid="+strconv.Itoa(os.Getpid()) || !strings.HasPrefix(lines[1], "started=") || lines[2] != "id=boot:777" {
		t.Fatalf("claim reads %q, want pid, started and id lines", lines)
	}
}

// A file system that cannot hard-link (FAT, some network mounts) makes every
// merge cold; the gate says so once, with the reason, instead of going quiet.
func TestWarmClaim_ALinkThatCannotBeMadeIsSaidOnce(t *testing.T) {
	var notes []string
	oldLink, oldNote := warmLink, warmNotef
	t.Cleanup(func() { warmLink, warmNotef = oldLink, oldNote })
	warmLink = func(string, string) error { return errors.New("operation not supported") }
	warmNotef = func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) }
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if takeWarmClaim(claim) {
		t.Fatal("a claim was taken although the link failed")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "operation not supported") || !strings.Contains(notes[0], claim) {
		t.Fatalf("notes = %q, want one line naming the claim and the link error", notes)
	}
}

// A merge that runs past the age bound keeps its claim while its pid and
// identity still match: the bound is only for a claim whose identity cannot be
// checked. Time is injected, so no test waits twelve hours.
func TestWarmClaim_ALongRunningMergeKeepsItsClaimWhileTheIdentityMatches(t *testing.T) {
	oldNow, oldID := warmNow, warmIdentityFn
	t.Cleanup(func() { warmNow, warmIdentityFn = oldNow, oldID })
	taken := time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	body := "pid=" + strconv.Itoa(os.Getpid()) + "\nstarted=" + taken.Format(time.RFC3339Nano) + "\nid=boot:5\n"
	warmNow = func() time.Time { return taken.Add(warmClaimMaxAge + time.Hour) }

	warmIdentityFn = func(int) (string, bool) { return "boot:5", true }
	if takeWarmClaim(warmWriteClaim(t, body)) {
		t.Fatal("a live merge with a matching identity lost its claim after the age bound")
	}
	// the identity cannot be read now: only then does the age bound decide
	warmIdentityFn = func(int) (string, bool) { return "", false }
	if !takeWarmClaim(warmWriteClaim(t, body)) {
		t.Fatal("a claim past the age bound with no readable identity still held the checkout")
	}
}

// The taker removes a stale claim only if what it removed is still the stale
// content. If a live claim got in between its check and its removal (the lock
// it held expired under it), it puts that claim back and gives up.
func TestWarmClaim_ALiveClaimThatAppearsBeforeTheRemovalIsPutBack(t *testing.T) {
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(claim, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := "pid=" + strconv.Itoa(os.Getpid()) + "\nstarted=" + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
	warmClaimBeforeRemove = func() {
		if err := os.WriteFile(claim, []byte(live), 0o644); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { warmClaimBeforeRemove = func() {} })
	if takeWarmClaim(claim) {
		t.Fatal("a taker removed a live claim and took the checkout")
	}
	got, err := os.ReadFile(claim)
	if err != nil || string(got) != live {
		t.Fatalf("the live claim was not left in place: %q (err %v)", got, err)
	}
}

// A takeover lock left by a taker that died inside its takeover is expired
// after a minute, and the taker that finds it goes on to take the claim.
func TestWarmClaim_AnExpiredTakeoverLockIsClearedAndTheClaimTaken(t *testing.T) {
	claim := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(claim, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := warmLockPath(claim)
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-warmTakeoverStale - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if !takeWarmClaim(claim) {
		t.Fatal("an expired takeover lock kept a stale claim from being taken")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the takeover lock outlived the takeover (stat err: %v)", err)
	}
	// a fresh lock still keeps a second taker out
	other := filepath.Join(t.TempDir(), "gate.claim")
	if err := os.WriteFile(other, []byte("pid="+strconv.Itoa(warmDeadPid(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(warmLockPath(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if takeWarmClaim(other) {
		t.Fatal("a fresh takeover lock did not keep the second taker out")
	}
}
