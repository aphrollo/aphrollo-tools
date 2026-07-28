package tdd

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// putFakeNextest prepends a dir holding a fake cargo-nextest executable to
// PATH. The binary is never executed — only exec.LookPath's verdict matters.
func putFakeNextest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	name := "cargo-nextest"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

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

	t.Run("source edit in a lib crate scopes to the lib target", func(t *testing.T) {
		root := mkProject(t, "Cargo.toml")
		write(t, root, filepath.Join("src", "lib.rs"), "")
		got := NarrowToRelatedTests(nextest, filepath.Join(root, "src", "game.rs"), root)
		want := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "--lib"}}
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
