package suite

import (
	"reflect"
	"testing"
)

// These are suite's own tests of gateprofile.go, reached today only through
// internal/tdd/precommit's gate tests.

const nextestWithGateProfile = "[profile.default]\nretries = 0\n\n[profile.gate]\nfail-fast = false\n"

// TestHasNextestProfile_FindsAnExactHeaderLine pins the positive case: the
// workspace's own .config/nextest.toml carries the table header.
func TestHasNextestProfile_FindsAnExactHeaderLine(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", nextestWithGateProfile)
	if !hasNextestProfile(ws, "gate") {
		t.Fatal("a nextest.toml with [profile.gate] has the gate profile")
	}
}

// TestHasNextestProfile_MatchesTheHeaderThroughSurroundingWhitespace pins that
// an indented or trailing-space header line still counts.
func TestHasNextestProfile_MatchesTheHeaderThroughSurroundingWhitespace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", "  [profile.gate]  \n")
	if !hasNextestProfile(ws, "gate") {
		t.Fatal("whitespace around a header line must not hide it")
	}
}

// TestHasNextestProfile_AnotherProfileIsNotThisOne pins that only the named
// table counts: a different profile, or the name inside a longer table name,
// is a miss.
func TestHasNextestProfile_AnotherProfileIsNotThisOne(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", "[profile.default]\n[profile.gated]\n# [profile.gate]\n")
	if hasNextestProfile(ws, "gate") {
		t.Fatal("neither [profile.gated] nor a commented header is [profile.gate]")
	}
}

// TestHasNextestProfile_NoConfigFileHasNoProfile pins the missing-file arm.
func TestHasNextestProfile_NoConfigFileHasNoProfile(t *testing.T) {
	t.Parallel()
	if hasNextestProfile(t.TempDir(), "gate") {
		t.Fatal("a workspace with no nextest.toml has no profile")
	}
}

// TestHasGateProfile_AsksForTheProfileCalledGate pins the name the gate looks
// for.
func TestHasGateProfile_AsksForTheProfileCalledGate(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", "[profile.ci]\n")
	if hasGateProfile(ws) {
		t.Fatal("[profile.ci] is not the gate profile")
	}
	write(t, ws, ".config/nextest.toml", nextestWithGateProfile)
	if !hasGateProfile(ws) {
		t.Fatal("[profile.gate] is the gate profile")
	}
}

// TestWithGateProfile_InsertsTheProfileRightAfterNextestRun pins the argv
// edit: `--profile gate` goes between `run` and the rest, and the rest keeps
// its order.
func TestWithGateProfile_InsertsTheProfileRightAfterNextestRun(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", nextestWithGateProfile)
	in := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "alpha"}, Dir: ws}
	got := withGateProfile(in, ws)
	want := []string{"nextest", "run", "--profile", "gate", "-p", "alpha"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("Args = %v, want %v", got.Args, want)
	}
	if !reflect.DeepEqual(in.Args, []string{"nextest", "run", "-p", "alpha"}) {
		t.Fatalf("the input runner's Args were edited in place: %v", in.Args)
	}
}

// TestWithGateProfile_LeavesAPlainCargoTestAlone pins that only a nextest run
// takes the flag: `cargo test` does not know --profile.
func TestWithGateProfile_LeavesAPlainCargoTestAlone(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, ".config/nextest.toml", nextestWithGateProfile)
	in := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}}
	if got := withGateProfile(in, ws); !reflect.DeepEqual(got, in) {
		t.Fatalf("withGateProfile = %+v, want the runner unchanged", got)
	}
}

// TestWithGateProfile_LeavesARunAloneWhenTheWorkspaceHasNoGateProfile pins the
// other guard: a nextest run in a workspace that never declared the profile
// must not be handed a profile nextest would reject.
func TestWithGateProfile_LeavesARunAloneWhenTheWorkspaceHasNoGateProfile(t *testing.T) {
	t.Parallel()
	in := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}
	if got := withGateProfile(in, t.TempDir()); !reflect.DeepEqual(got, in) {
		t.Fatalf("withGateProfile = %+v, want the runner unchanged", got)
	}
}

// TestIsNextestRun_NeedsBothWords pins the predicate: `nextest run` in the
// first two positions, and not `nextest list` or a bare `nextest`.
func TestIsNextestRun_NeedsBothWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"nextest", "run"}, true},
		{[]string{"nextest", "run", "-p", "a"}, true},
		{[]string{"nextest", "list"}, false},
		{[]string{"nextest"}, false},
		{[]string{"test", "run"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := isNextestRun(c.args); got != c.want {
			t.Errorf("isNextestRun(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
