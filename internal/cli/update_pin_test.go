package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rollback"
)

// A pin is only worth anything if the habit of typing `aphrollo update` cannot
// drop it, so a plain update says what it is pinned to and stops before any
// network or build.
func TestUpdate_PlainWhilePinnedNamesThePinAndDoesNothing(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	if err := rollback.WritePin(rollback.Pin{Ref: "v1.0.0", Commit: r.c1}); err != nil {
		t.Fatal(err)
	}
	bin := installedBin(t, "OLD")
	moved := gitOutput(t, r.git, r.clone, "rev-parse", "origin/main")

	code, out, errb := update(t, r, bin)

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	for _, want := range []string{"pinned to v1.0.0 (" + r.c1[:7] + ")", "[skip]", "aphrollo update --unpin", "aphrollo update --to"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "aphrollo update") {
			t.Errorf("line %q does not carry the verb's prefix", line)
		}
	}
	if len(*built) != 0 || binContent(t, bin) != "OLD" {
		t.Fatalf("a pinned plain update built %v or replaced the binary", *built)
	}
	if got := gitOutput(t, r.git, r.clone, "rev-parse", "origin/main"); got != moved {
		t.Fatalf("a pinned plain update fetched: origin/main moved %s -> %s", moved, got)
	}
}

func TestUpdate_UnpinClearsThePinAndReturnsToOriginMain(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	if err := rollback.WritePin(rollback.Pin{Ref: "v1.0.0", Commit: r.c1}); err != nil {
		t.Fatal(err)
	}
	bin := installedBin(t, "built:"+r.c1)

	code, out, errb := update(t, r, bin, "--unpin")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if len(*built) != 1 || (*built)[0] != r.c2 {
		t.Fatalf("built %v, want origin/main %s", *built, r.c2)
	}
	if _, state := rollback.ReadPin(); state != rollback.Unpinned {
		t.Fatal("the pin is still set after --unpin")
	}
	if !strings.Contains(out, "unpin") || !strings.Contains(out, "v1.0.0 ("+r.c1[:7]+")") {
		t.Fatalf("stdout does not say which pin was cleared:\n%s", out)
	}
}

func TestUpdate_UnpinOnAnUnpinnedBoxSaysSoAndStillUpdates(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")

	code, out, errb := update(t, r, bin, "--unpin")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "not pinned [skip]") {
		t.Fatalf("stdout does not say there was no pin:\n%s", out)
	}
	if len(*built) != 1 || (*built)[0] != r.c2 {
		t.Fatalf("built %v, want origin/main %s", *built, r.c2)
	}
	if len(updateEvents(t, "unpin")) != 0 {
		t.Fatal("an unpin of nothing was logged")
	}
}

// The pin stands for the binary on the box. If getting back to origin/main
// fails, the box still runs the pinned one and must still say so.
func TestUpdate_UnpinKeepsThePinWhenTheBuildFails(t *testing.T) {
	r := rollbackFixture(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) { return "go build", os.ErrInvalid }
	t.Cleanup(func() { buildAphrollo = prev })
	if err := rollback.WritePin(rollback.Pin{Ref: "v1.0.0", Commit: r.c1}); err != nil {
		t.Fatal(err)
	}

	code, _, _ := update(t, r, installedBin(t, "OLD"), "--unpin")

	if code == 0 {
		t.Fatal("a failed build must not report success")
	}
	if pin, state := rollback.ReadPin(); state != rollback.Pinned || pin.Commit != r.c1 {
		t.Fatalf("pin = (%+v, %v), want it kept", pin, state)
	}
	if len(updateEvents(t, "unpin")) != 0 {
		t.Fatal("an unpin that did not happen was logged")
	}
}

func TestUpdate_ToDoesNotPinWhenTheSwapIsRefused(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	prev := runSmokeCheckFn
	runSmokeCheckFn = func(string) error { return os.ErrInvalid }
	t.Cleanup(func() { runSmokeCheckFn = prev })
	bin := installedBin(t, "OLD")

	code, _, _ := update(t, r, bin, "--to", "v1.0.0")

	if code == 0 {
		t.Fatal("a candidate that fails its smoke check must not report success")
	}
	if _, state := rollback.ReadPin(); state != rollback.Unpinned {
		t.Fatal("a swap that did not happen left a pin")
	}
	if binContent(t, bin) != "OLD" {
		t.Fatal("the binary changed despite the refused swap")
	}
}

func TestUpdate_APinFileFromANewerAphrolloRefusesBeforeAnyWork(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	pinFile := rollback.PinPath()
	if err := os.MkdirAll(filepath.Dir(pinFile), 0o755); err != nil {
		t.Fatal(err)
	}
	newer := `{"schema":2,"ref":"v9","commit":"` + r.c1 + `"}`
	mustWriteFile(t, pinFile, newer)

	for _, flags := range [][]string{{}, {"--unpin"}, {"--to", "v1.0.0"}} {
		code, _, errb := update(t, r, installedBin(t, "OLD"), flags...)

		if code != 1 || !strings.Contains(errb, "newer aphrollo") {
			t.Fatalf("update %v: exit %d, stderr %q; want 1 naming the newer aphrollo", flags, code, errb)
		}
	}
	if len(*built) != 0 {
		t.Fatalf("built %v before refusing", *built)
	}
	if got, _ := os.ReadFile(pinFile); string(got) != newer {
		t.Fatalf("the newer pin file changed: %q", got)
	}
}

func TestUpdate_DryToPreviewsThePinAndChangesNothing(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")

	code, out, errb := update(t, r, bin, "--to", "v1.0.0", "--dry")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	for _, want := range []string{"dry run", "resolve v1.0.0", "pin:   would pin the box to v1.0.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if len(*built) != 0 || binContent(t, bin) != "OLD" {
		t.Fatalf("--dry built %v or replaced the binary", *built)
	}
	if _, state := rollback.ReadPin(); state != rollback.Unpinned {
		t.Fatal("--dry wrote a pin")
	}
	if len(loggedEvents(t)) != 0 {
		t.Fatalf("--dry wrote events: %+v", loggedEvents(t))
	}
}

func TestUpdate_DryUnpinNamesThePinItWouldClear(t *testing.T) {
	r := rollbackFixture(t)
	if err := rollback.WritePin(rollback.Pin{Ref: "v1.0.0", Commit: r.c1}); err != nil {
		t.Fatal(err)
	}

	code, out, _ := update(t, r, installedBin(t, "OLD"), "--unpin", "--dry")

	if code != 0 || !strings.Contains(out, "pin:   would clear the pin on v1.0.0 ("+r.c1[:7]+")") {
		t.Fatalf("exit %d, plan:\n%s", code, out)
	}
	if _, state := rollback.ReadPin(); state != rollback.Pinned {
		t.Fatal("--dry cleared the pin")
	}
}

func TestUpdate_DryUnpinOnAnUnpinnedBoxSaysThereIsNothingToClear(t *testing.T) {
	r := rollbackFixture(t)

	code, out, _ := update(t, r, installedBin(t, "OLD"), "--unpin", "--dry")

	if code != 0 || !strings.Contains(out, "pin:   not pinned; --unpin would clear nothing") {
		t.Fatalf("exit %d, plan:\n%s", code, out)
	}
}

func TestUpdate_DryListsTheBinariesKeptForRollback(t *testing.T) {
	r := rollbackFixture(t)
	bin := installedBin(t, "OLD")
	keptFile := filepath.Join(filepath.Dir(bin), "aphrollo.stale-1700000001.exe")
	mustWriteFile(t, keptFile, "KEPT")
	in := rollback.OpenInstalls(filepath.Dir(bin), "aphrollo.exe")
	in.Put(rollback.Binary{File: "aphrollo.stale-1700000001.exe", Commit: r.c1, BuiltAt: "2026-09-30T08:00:00Z", Ref: "v1.0.0", SHA256: "x"})
	if err := in.Save(); err != nil {
		t.Fatal(err)
	}

	_, out, _ := update(t, r, bin, "--dry")

	want := "kept:  " + r.c1[:7] + " built 2026-09-30T08:00:00Z (aphrollo.stale-1700000001.exe)"
	if !strings.Contains(out, want) {
		t.Fatalf("plan lacks %q:\n%s", want, out)
	}
}

func TestUpdate_WritesTheSwapPinAndUnpinToTheEventLog(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	if code, out, errb := update(t, r, bin, "--to", "v1.0.0"); code != 0 {
		t.Fatalf("--to: exit %d\n%s%s", code, out, errb)
	}
	if code, out, errb := update(t, r, bin, "--unpin"); code != 0 {
		t.Fatalf("--unpin: exit %d\n%s%s", code, out, errb)
	}

	swaps := updateEvents(t, "swap")
	if len(swaps) != 2 {
		t.Fatalf("swap events = %+v, want one per swap", swaps)
	}
	if d := swaps[0].Detail; d["how"] != "build" || d["to"] != r.c1 || d["ref"] != "v1.0.0" || swaps[0].Verdict != "ok" {
		t.Fatalf("first swap = %+v, want a build of %s for v1.0.0", swaps[0], r.c1)
	}
	if d := swaps[1].Detail; d["how"] != "build" || d["from"] != r.c1 || d["to"] != r.c2 {
		t.Fatalf("second swap = %+v, want %s -> %s", swaps[1], r.c1, r.c2)
	}
	pins := updateEvents(t, "pin")
	if len(pins) != 1 || pins[0].Verdict != "set" || pins[0].Detail["ref"] != "v1.0.0" || pins[0].Detail["commit"] != r.c1 {
		t.Fatalf("pin events = %+v, want one set for v1.0.0 at %s", pins, r.c1)
	}
	unpins := updateEvents(t, "unpin")
	if len(unpins) != 1 || unpins[0].Verdict != "cleared" || unpins[0].Detail["ref"] != "v1.0.0" || unpins[0].Detail["commit"] != r.c1 {
		t.Fatalf("unpin events = %+v, want one clearing v1.0.0 at %s", unpins, r.c1)
	}
}

func TestUpdate_ASwitchToAKeptCopyIsLoggedAsOne(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	for _, flags := range [][]string{{"--to", "v1.0.0"}, {"--unpin"}, {"--to", "v1.0.0"}} {
		if code, out, errb := update(t, r, bin, flags...); code != 0 {
			t.Fatalf("update %v: exit %d\n%s%s", flags, code, out, errb)
		}
	}

	swaps := updateEvents(t, "swap")

	if len(swaps) != 3 || swaps[2].Detail["how"] != "kept" || swaps[2].Detail["to"] != r.c1 {
		t.Fatalf("swap events = %+v, want the third to be a switch to the kept %s", swaps, r.c1)
	}
}

// The record beside each binary: the outgoing binary's commit and build stamp
// come from the running process when it IS that binary, the incoming one's from
// the build just made.
func TestUpdate_RecordsEachBinaryBesideItsCopy(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	updateAt(t, "2026-10-02T10:00:00Z")
	builtAt := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	prevClock := buildClock
	buildClock = func() time.Time { return builtAt }
	t.Cleanup(func() { buildClock = prevClock })
	bin := installedBin(t, "OLD")
	fakeRunningAs(t, bin, bin)
	const outgoingCommit = "c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0"
	stampAs(t, outgoingCommit, "2026-09-01T00:00:00Z")

	code, out, errb := update(t, r, bin, "--to", "v1.0.0")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	dir := filepath.Dir(bin)
	in := rollback.OpenInstalls(dir, "aphrollo.exe")
	sum, err := rollback.FileSHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	active, ok := in.Get("aphrollo.exe")
	want := rollback.Binary{File: "aphrollo.exe", Commit: r.c1, BuiltAt: "2026-10-02T09:30:00Z", Ref: "v1.0.0", SHA256: sum, InstalledAt: "2026-10-02T10:00:00Z"}
	if !ok || active != want {
		t.Fatalf("active record = (%+v, %v), want %+v", active, ok, want)
	}
	others := in.Others("aphrollo.exe")
	if len(others) != 1 {
		t.Fatalf("records beside the copies = %+v, want the one outgoing binary", others)
	}
	if others[0].Commit != outgoingCommit || others[0].BuiltAt != "2026-09-01T00:00:00Z" {
		t.Fatalf("outgoing record = %+v, want the running process's own stamp", others[0])
	}
	if body := binContent(t, filepath.Join(dir, others[0].File)); body != "OLD" {
		t.Fatalf("the outgoing record names %s, which holds %q, not the replaced binary", others[0].File, body)
	}
}

// A binary nothing can name is simply not recorded: a guess would mislabel the
// copy a rollback later trusts.
func TestUpdate_RecordsNoCommitForAnOutgoingBinaryItCannotName(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	stampAs(t, "c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0", "2026-09-01T00:00:00Z") // the process is not bin

	if code, out, errb := update(t, r, bin, "--to", "v1.0.0"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}

	in := rollback.OpenInstalls(filepath.Dir(bin), "aphrollo.exe")
	if others := in.Others("aphrollo.exe"); len(others) != 0 {
		t.Fatalf("records for copies = %+v, want none", others)
	}
}

// What was recorded for the installed binary describes bytes that have since
// been replaced under it, so it does not follow the copy.
func TestUpdate_AnOutgoingRecordThatNoLongerMatchesItsBytesIsNotCarried(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	bin := installedBin(t, "REPLACED BY HAND")
	in := rollback.OpenInstalls(filepath.Dir(bin), "aphrollo.exe")
	in.Put(rollback.Binary{File: "aphrollo.exe", Commit: r.c1, SHA256: "0000"})
	if err := in.Save(); err != nil {
		t.Fatal(err)
	}

	if code, out, errb := update(t, r, bin, "--to", "v1.0.0"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}

	after := rollback.OpenInstalls(filepath.Dir(bin), "aphrollo.exe")
	if others := after.Others("aphrollo.exe"); len(others) != 0 {
		t.Fatalf("records for copies = %+v, want the stale record dropped", others)
	}
}

// The record never names a copy the retention has reclaimed.
func TestUpdate_TheRecordFollowsTheCopiesTheRetentionKeeps(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	dir := filepath.Dir(bin)
	in := rollback.OpenInstalls(dir, "aphrollo.exe")
	for _, n := range []string{"1700000001", "1700000002", "1700000003"} {
		f := "aphrollo.stale-" + n + ".exe"
		mustWriteFile(t, filepath.Join(dir, f), "OLDER"+n)
		in.Put(rollback.Binary{File: f, Commit: r.c1, SHA256: "x"})
	}
	if err := in.Save(); err != nil {
		t.Fatal(err)
	}

	if code, out, errb := update(t, r, bin, "--to", "v1.0.0"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}

	after := rollback.OpenInstalls(dir, "aphrollo.exe")
	for _, b := range after.Binaries {
		if _, err := os.Stat(filepath.Join(dir, b.File)); err != nil {
			t.Errorf("the record names %s, which is gone: %v", b.File, err)
		}
	}
	if _, ok := after.Get("aphrollo.stale-1700000003.exe"); !ok {
		t.Errorf("the newest earlier copy lost its record: %+v", after.Binaries)
	}
	if _, ok := after.Get("aphrollo.stale-1700000001.exe"); ok {
		t.Errorf("a reclaimed copy is still in the record: %+v", after.Binaries)
	}
}

func TestPinBuildClock_FreezesTheStampInstantAndRestoresTheClock(t *testing.T) {
	ticks := []time.Time{time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 9, 5, 0, 0, time.UTC)}
	next := 0
	prev := buildClock
	buildClock = func() time.Time { next++; return ticks[min(next-1, len(ticks)-1)] }
	t.Cleanup(func() { buildClock = prev })

	at, restore := pinBuildClock()
	first, second := buildClock(), buildClock()
	restore()

	if !at.Equal(ticks[0]) || !first.Equal(ticks[0]) || !second.Equal(ticks[0]) {
		t.Fatalf("pinned at %v, then read %v and %v, want %v throughout", at, first, second, ticks[0])
	}
	if got := buildClock(); !got.Equal(ticks[1]) {
		t.Fatalf("after restore the clock read %v, want the live clock's %v", got, ticks[1])
	}
}
