package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/rollback"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `aphrollo update --to <ref>` installs an earlier (or any) point of origin/main
// and pins the box to it; a plain update then leaves the pin alone and
// `--unpin` is the one way back. A rolled-back-from binary stays among the last
// three, so going back and forth costs no build.

// rollbackRepo is an origin with two commits on main, the older one tagged
// v1.0.0, and a clone of it that has not fetched the newer one yet.
type rollbackRepo struct {
	clone  string
	git    string
	c1, c2 string
}

func rollbackFixture(t *testing.T) rollbackRepo {
	t.Helper()
	gateConfigDir(t)
	_, clone, seed := updateFixture(t)
	git := realGitForTest(t)
	c1 := gitOutput(t, git, seed, "rev-parse", "HEAD")
	gitOutput(t, git, seed, "tag", "v1.0.0")
	mustWriteFile(t, filepath.Join(seed, "second.txt"), "2")
	gitOutput(t, git, seed, "add", "-A")
	gitOutput(t, git, seed, "commit", "-q", "-m", "second")
	gitOutput(t, git, seed, "push", "-q", "origin", "main")
	gitOutput(t, git, seed, "push", "-q", "origin", "v1.0.0")
	c2 := gitOutput(t, git, seed, "rev-parse", "HEAD")
	return rollbackRepo{clone: clone, git: git, c1: c1, c2: c2}
}

// stubBuilds replaces the build with one that writes "built:<commit>" and
// records the commit of each worktree it was asked to build.
func stubBuilds(t *testing.T, git string) *[]string {
	t.Helper()
	built := new([]string)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		head := gitOutput(t, git, repo, "rev-parse", "HEAD")
		*built = append(*built, head)
		return "go build", os.WriteFile(out, []byte("built:"+head), 0o755)
	}
	t.Cleanup(func() { buildAphrollo = prev })
	return built
}

// updateAt fixes the clock the pin and the install record read.
func updateAt(t *testing.T, at string) {
	t.Helper()
	instant, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	prev := updateNow
	updateNow = func() time.Time { return instant }
	t.Cleanup(func() { updateNow = prev })
}

func installedBin(t *testing.T, content string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func update(t *testing.T, r rollbackRepo, bin string, flags ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"--repo", r.clone, "--bin", bin, "--no-init"}, flags...)
	code = runUpdate(args, &out, &errb)
	return code, out.String(), errb.String()
}

func binContent(t *testing.T, bin string) string {
	t.Helper()
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func updateEvents(t *testing.T, stage string) []tdd.Event {
	t.Helper()
	var out []tdd.Event
	for _, e := range eventsOfKind(loggedEvents(t), "update") {
		if e.Stage == stage {
			out = append(out, e)
		}
	}
	return out
}

// stampAs makes the running process look like the binary built from commit.
func stampAs(t *testing.T, commit, builtAt string) {
	t.Helper()
	buildinfo.SetForTest(commit, builtAt)
	t.Cleanup(func() { buildinfo.SetForTest("", "") })
}

func TestUpdate_ToATagBuildsThatCommitAndPinsTheBoxToIt(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	updateAt(t, "2026-10-02T10:00:00Z")
	bin := installedBin(t, "OLD")

	code, out, errb := update(t, r, bin, "--to", "v1.0.0")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errb)
	}
	if len(*built) != 1 || (*built)[0] != r.c1 {
		t.Fatalf("built %v, want exactly the tag's commit %s (origin/main is %s)", *built, r.c1, r.c2)
	}
	if got := binContent(t, bin); got != "built:"+r.c1 {
		t.Fatalf("bin = %q, want the build of %s", got, r.c1)
	}
	pin, state := rollback.ReadPin()
	want := rollback.Pin{Schema: 1, Ref: "v1.0.0", Commit: r.c1, At: "2026-10-02T10:00:00Z"}
	if state != rollback.Pinned || pin != want {
		t.Fatalf("pin = (%+v, %v), want %+v", pin, state, want)
	}
	if want := "pinned to v1.0.0 (" + r.c1[:7] + ")"; !strings.Contains(out, want) {
		t.Fatalf("stdout lacks %q:\n%s", want, out)
	}
}

func TestUpdate_ToACommitShaBuildsThatCommitAndPinsItByItsShortSha(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")

	code, out, errb := update(t, r, bin, "--to", r.c1[:10])

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errb)
	}
	if len(*built) != 1 || (*built)[0] != r.c1 {
		t.Fatalf("built %v, want %s", *built, r.c1)
	}
	pin, _ := rollback.ReadPin()
	if pin.Ref != r.c1[:7] || pin.Commit != r.c1 {
		t.Fatalf("pin = %+v, want ref %s and commit %s", pin, r.c1[:7], r.c1)
	}
}

// update --to installs what is merged on the branch the box follows, never a
// local branch, an unpushed commit or a tag nobody pushed: that is the fence
// `gate self-install` was retired over.
func TestUpdate_ToRefusesWhatIsNotAnOriginCommitOrTag(t *testing.T) {
	r := rollbackFixture(t)
	mustWriteFile(t, filepath.Join(r.clone, "local.txt"), "x")
	gitOutput(t, r.git, r.clone, "add", "-A")
	gitOutput(t, r.git, r.clone, "commit", "-q", "-m", "unpushed")
	local := gitOutput(t, r.git, r.clone, "rev-parse", "HEAD")
	gitOutput(t, r.git, r.clone, "tag", "local-only")

	cases := []struct{ name, ref, want string }{
		{"an unpushed commit", local, "not on origin/main"},
		{"a tag that was never pushed", "local-only", "not on origin/main"},
		{"a branch name", "main", "neither a tag nor a commit sha"},
		{"nothing at all", "nosuch", "neither a tag nor a commit sha"},
		{"a sha git does not know", "deadbeefdeadbeef", "no commit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			built := stubBuilds(t, r.git)
			bin := installedBin(t, "OLD")

			code, _, errb := update(t, r, bin, "--to", c.ref)

			if code != 1 || !strings.Contains(errb, c.want) {
				t.Fatalf("exit %d, stderr %q; want 1 saying %q", code, errb, c.want)
			}
			if len(*built) != 0 || binContent(t, bin) != "OLD" {
				t.Fatalf("a refused ref built %v or changed the binary", *built)
			}
			if _, state := rollback.ReadPin(); state != rollback.Unpinned {
				t.Fatal("a refused ref left a pin")
			}
		})
	}
}

func TestUpdate_ToAnOptionLookingRefIsAUsageError(t *testing.T) {
	r := rollbackFixture(t)

	code, _, errb := update(t, r, installedBin(t, "OLD"), "--to=-x")

	if code != 2 || !strings.Contains(errb, "--to") {
		t.Fatalf("exit %d, stderr %q; want 2 naming --to", code, errb)
	}
}

func TestUpdate_ToAndUnpinTogetherAreAUsageError(t *testing.T) {
	r := rollbackFixture(t)

	code, _, errb := update(t, r, installedBin(t, "OLD"), "--to", "v1.0.0", "--unpin")

	if code != 2 || !strings.Contains(errb, "--to") || !strings.Contains(errb, "--unpin") {
		t.Fatalf("exit %d, stderr %q; want 2 naming both flags", code, errb)
	}
}

func TestUpdate_ToAKeptBinarySwitchesToItWithoutBuilding(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	for _, flags := range [][]string{{"--to", "v1.0.0"}, {"--unpin"}} {
		if code, out, errb := update(t, r, bin, flags...); code != 0 {
			t.Fatalf("update %v: exit %d\n%s%s", flags, code, out, errb)
		}
	}
	if got := binContent(t, bin); got != "built:"+r.c2 {
		t.Fatalf("after --unpin bin = %q, want the build of origin/main %s", got, r.c2)
	}

	code, out, errb := update(t, r, bin, "--to", "v1.0.0")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if len(*built) != 2 {
		t.Fatalf("built %v: going back to v1.0.0 must reuse the kept copy, not build a third time", *built)
	}
	if got := binContent(t, bin); got != "built:"+r.c1 {
		t.Fatalf("bin = %q, want the kept build of %s", got, r.c1)
	}
	if !strings.Contains(out, "switch") {
		t.Fatalf("stdout does not say it switched to a kept copy:\n%s", out)
	}
	if pin, state := rollback.ReadPin(); state != rollback.Pinned || pin.Commit != r.c1 {
		t.Fatalf("pin = (%+v, %v), want the box pinned to %s again", pin, state, r.c1)
	}
	if kept := staleCopies(t, filepath.Dir(bin)); len(kept) != 2 {
		t.Fatalf("stale copies = %v, want the two other binaries kept", kept)
	}
}

// And the way back needs no build either: the binary rolled back FROM is kept.
func TestUpdate_UnpinSwitchesBackToTheKeptOriginMainBinary(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	for _, flags := range [][]string{{}, {"--to", "v1.0.0"}} { // origin/main first, then back to the tag
		if code, out, errb := update(t, r, bin, flags...); code != 0 {
			t.Fatalf("update %v: exit %d\n%s%s", flags, code, out, errb)
		}
	}

	code, out, errb := update(t, r, bin, "--unpin")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if len(*built) != 2 {
		t.Fatalf("built %v, want the kept origin/main binary reused", *built)
	}
	if got := binContent(t, bin); got != "built:"+r.c2 {
		t.Fatalf("bin = %q, want the kept build of %s", got, r.c2)
	}
}

// A kept copy is trusted only while its bytes are the ones that were recorded.
func TestUpdate_ToAKeptCopyWhoseBytesChangedBuildsAgain(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	bin := installedBin(t, "OLD")
	for _, flags := range [][]string{{"--to", "v1.0.0"}, {"--unpin"}} {
		if code, out, errb := update(t, r, bin, flags...); code != 0 {
			t.Fatalf("update %v: exit %d\n%s%s", flags, code, out, errb)
		}
	}
	for _, kept := range staleCopies(t, filepath.Dir(bin)) {
		if binContent(t, kept) == "built:"+r.c1 {
			mustWriteFile(t, kept, "tampered")
		}
	}

	code, out, errb := update(t, r, bin, "--to", "v1.0.0")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if len(*built) != 3 || (*built)[2] != r.c1 {
		t.Fatalf("built %v, want v1.0.0 built again from source", *built)
	}
	if got := binContent(t, bin); got != "built:"+r.c1 {
		t.Fatalf("bin = %q, want the fresh build", got)
	}
}

func TestUpdate_ToTheCommitTheBinaryIsAtSkipsTheSwapButStillPins(t *testing.T) {
	r := rollbackFixture(t)
	built := stubBuilds(t, r.git)
	stampAs(t, r.c1, "2026-01-01T00:00:00Z")
	bin := installedBin(t, "OLD")

	code, out, errb := update(t, r, bin, "--to", "v1.0.0")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	if want := "already at v1.0.0 (" + r.c1[:7] + ") [skip]"; !strings.Contains(out, want) {
		t.Fatalf("stdout lacks %q:\n%s", want, out)
	}
	if len(*built) != 0 || binContent(t, bin) != "OLD" {
		t.Fatalf("a binary already at the target was rebuilt (%v) or replaced", *built)
	}
	if pin, state := rollback.ReadPin(); state != rollback.Pinned || pin.Commit != r.c1 {
		t.Fatalf("pin = (%+v, %v), want a pin on %s", pin, state, r.c1)
	}
	if len(updateEvents(t, "swap")) != 0 || len(updateEvents(t, "pin")) != 1 {
		t.Fatalf("events = %+v, want a pin and no swap", loggedEvents(t))
	}
}

func TestUpdate_ToTheRefAlreadyPinnedChangesNothingAndLogsNothing(t *testing.T) {
	r := rollbackFixture(t)
	stubBuilds(t, r.git)
	stampAs(t, r.c1, "2026-01-01T00:00:00Z")
	bin := installedBin(t, "OLD")
	if code, out, errb := update(t, r, bin, "--to", "v1.0.0"); code != 0 {
		t.Fatalf("first --to: exit %d\n%s%s", code, out, errb)
	}
	before := len(loggedEvents(t))

	code, out, errb := update(t, r, bin, "--to", "v1.0.0")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errb)
	}
	for _, want := range []string{"already at", "already pinned to v1.0.0 (" + r.c1[:7] + ") [skip]"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if after := len(loggedEvents(t)); after != before {
		t.Fatalf("a skipped run wrote %d event(s)", after-before)
	}
}
