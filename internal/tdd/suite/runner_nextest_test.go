package suite

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func putFakeNextest(t *testing.T) { t.Helper(); tddtest.PutFakeNextest(t) }

// dropNextestFromPath empties PATH so LookPath cannot find cargo-nextest even
// on a box that has it installed.
func dropNextestFromPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// A cargo project that carries a nextest config AND has cargo-nextest
// installed gets the nextest runner — 2-3x on link-heavy suites is the whole
// point of the gate diet. Without either half, plain `cargo test` stands.
func TestDetectRunner_CargoNextest(t *testing.T) {
	t.Run("config plus installed binary selects nextest", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		write(t, root, filepath.Join(".config", "nextest.toml"), "[profile.default]\n")
		putFakeNextest(t)
		r, ok := DetectRunner(root)
		if !ok {
			t.Fatal("cargo project must detect a runner")
		}
		want := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("want %+v, got %+v", want, r)
		}
	})

	t.Run("config without the binary falls back to cargo test", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		write(t, root, filepath.Join(".config", "nextest.toml"), "[profile.default]\n")
		dropNextestFromPath(t)
		r, _ := DetectRunner(root)
		want := Runner{Cmd: "cargo", Args: []string{"test"}}
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("a missing cargo-nextest binary must fall back to %+v, got %+v", want, r)
		}
	})

	t.Run("no config keeps cargo test", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		putFakeNextest(t)
		r, _ := DetectRunner(root)
		want := Runner{Cmd: "cargo", Args: []string{"test"}}
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("without .config/nextest.toml the runner must stay %+v, got %+v", want, r)
		}
	})
}

// Narrowing must preserve the nextest verb: scoping a nextest runner back to
// `cargo test …` would silently forfeit the speedup at exactly the runs the
// diet targets (commit-time crate scoping, per-edit test-target scoping).
func TestNarrowing_PreservesNextest(t *testing.T) {
	nextest := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	t.Run("staged crate scoping", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n")
		scoped, narrowed := narrowToStaged(nextest, root, []string{"src/lib.rs"})
		if !narrowed {
			t.Fatal("cargo narrowing must apply to the nextest runner too")
		}
		want := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "m"}}
		if !reflect.DeepEqual(scoped, want) {
			t.Fatalf("want %+v, got %+v", want, scoped)
		}
	})

	t.Run("test-file edit scopes to one test target", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		got := NarrowToRelatedTests(nextest, filepath.Join(root, "tests", "combat.rs"), root)
		want := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "--test", "combat"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("want %+v, got %+v", want, got)
		}
	})

	t.Run("source edit in a lib crate scopes to the lib target and its module", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		write(t, root, filepath.Join("src", "lib.rs"), "")
		got := NarrowToRelatedTests(nextest, filepath.Join(root, "src", "game.rs"), root)
		want := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "--lib", "-E", "test(/^game::/)"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("want %+v, got %+v", want, got)
		}
	})
}

// nextest's failure line differs from libtest's (`FAIL [ 0.4s] binary-id
// test::name`, not `test name ... FAILED`); the extractor must parse it or
// every nextest rejection reads "no failing test parsed".
func TestExtractFailingTests_Nextest(t *testing.T) {
	out := `
        PASS [   0.310s] server::integration your_progress::feed_reaches_owner
        FAIL [   2.043s] server::integration xp_award::nonlethal_damage_awards_nothing
        FAIL [   0.101s] shared apply_movement::grounded_stays_grounded
     Summary [ 136.630s] 105 tests run: 103 passed, 2 failed, 3 skipped
`
	got := ExtractFailingTests(out)
	want := []string{
		"apply_movement::grounded_stays_grounded",
		"xp_award::nonlethal_damage_awards_nothing",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// The integration-test shape #590 reports. nextest narrating progress puts an
// `(n/m)` counter between the duration and the binary id, and a test living in
// an integration-test binary carries a binary id of its own
// (`pose_ik::integration`) BEFORE its `module::name` path. The failing test's
// name is the LAST whitespace-separated field on the line; reading the field at
// a fixed offset from the duration instead picks up the binary id, which no
// --want-fail can ever match, and the proof is ruled WRONG FAILURE against
// evidence that never named a test at all.
func TestExtractFailingTests_ReadsNextestFailLines(t *testing.T) {
	out := "        FAIL [ 0.020s] (1/1) pose_ik::integration solve_clip_bin::solve_clip_exits_nonzero_on_bad_argv\n" +
		"   Summary [ 0.021s] 1 test run: 0 passed, 1 failed, 40 skipped\n"

	got := ExtractFailingTests(out)

	want := []string{"solve_clip_bin::solve_clip_exits_nonzero_on_bad_argv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractFailingTests = %#v, want %#v", got, want)
	}
}
