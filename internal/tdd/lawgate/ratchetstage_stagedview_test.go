package lawgate

import (
	"path/filepath"
	"strings"
	"testing"
)

// A whole-tree law reads more than the file a hit lands in: a registry file,
// a superset file, a package sibling, a bench record. At commit time every one
// of those reads has to come from the STAGED tree, the same view the scan
// already judges the other side from. A read that goes to disk instead mixes
// two trees, and the law then judges a commit that does not exist.

// registryTree is a committed repo whose README registers exactly the verbs
// cli.go dispatches: `a`, and nothing else.
func registryTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "verbs.toml"), `
name = "verbs"
description = "Every dispatched verb is in the README and every README verb is dispatched"
severity = "deny"
baseline = ".ratchet/baselines/verbs.txt"

[scope]
include = ["README.md", "cli.go"]
min_files = 2

[matcher]
kind = "registry-both-ways"
registry_file = "README.md"
entry_pattern = "aphrollo ([a-z]+)"
use_pattern = "case \"([a-z]+)\":"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "verbs.txt"), "")
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\n")
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\n}\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

// The reported shape: the commit stages neither README nor cli.go, and both
// carry an unstaged new verb. The staged tree is the committed one and is
// consistent, so there is nothing to refuse.
func TestRatchetStage_RegistryIgnoresAVerbUnstagedOnBothSides(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\nrun aphrollo b\n")
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\ncase \"b\":\n}\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("neither side of the new verb is staged, the staged tree is consistent: %s", res.Message)
	}
}

// One side only, unstaged, on the registry side: still not in this commit.
func TestRatchetStage_RegistryIgnoresAnUnstagedRegistryEntry(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\nrun aphrollo b\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an unstaged README line is not part of the commit: %s", res.Message)
	}
}

// One side only, unstaged, on the use side.
func TestRatchetStage_RegistryIgnoresAnUnstagedUse(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\ncase \"b\":\n}\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an unstaged dispatch case is not part of the commit: %s", res.Message)
	}
}

// The converse, use side staged: the commit dispatches `b` and registers
// nothing for it. An unstaged README line that would register it does not
// land with this commit, so it must not excuse it.
func TestRatchetStage_RegistryRefusesAStagedUseRegisteredOnlyOnDisk(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\ncase \"b\":\n}\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\nrun aphrollo b\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "b is used but not in README.md") {
		t.Fatalf("the staged tree dispatches an unregistered verb: %+v", res)
	}
}

// The converse, registry side staged: the commit registers `b` and nothing
// it contains uses it.
func TestRatchetStage_RegistryRefusesAStagedEntryUsedOnlyOnDisk(t *testing.T) {
	root := registryTree(t)
	mustWrite(t, filepath.Join(root, "README.md"), "run aphrollo a\nrun aphrollo b\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "cli.go"), "switch v {\ncase \"a\":\ncase \"b\":\n}\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "b is registered but nothing uses it") {
		t.Fatalf("the staged tree registers a verb nothing uses: %+v", res)
	}
}

// containmentTree is a committed repo whose subset file claims only what its
// superset file carries.
func containmentTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "contained.toml"), `
name = "contained"
description = "The stand-in refuses at least what the real system refuses"
severity = "deny"
baseline = ".ratchet/baselines/contained.txt"

[scope]
include = ["real.txt", "standin.txt"]

[matcher]
kind = "file-set-containment"
superset_file = "real.txt"
subset_file = "standin.txt"
capture = "item ([a-z]+)"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "contained.txt"), "")
	mustWrite(t, filepath.Join(root, "real.txt"), "item a\n")
	mustWrite(t, filepath.Join(root, "standin.txt"), "item a\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

func TestRatchetStage_ContainmentIgnoresAnUnstagedSubsetClaim(t *testing.T) {
	root := containmentTree(t)
	mustWrite(t, filepath.Join(root, "standin.txt"), "item a\nitem b\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an unstaged subset line is not part of the commit: %s", res.Message)
	}
}

func TestRatchetStage_ContainmentRefusesAStagedClaimCoveredOnlyOnDisk(t *testing.T) {
	root := containmentTree(t)
	mustWrite(t, filepath.Join(root, "standin.txt"), "item a\nitem b\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "real.txt"), "item a\nitem b\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, `missing "b"`) {
		t.Fatalf("the staged superset lacks b, which the staged subset claims: %+v", res)
	}
}

// A marker-in-package trigger is excused only by a sibling the commit
// carries. An untracked sibling on disk is part of no commit.
func TestRatchetStage_PackageMarkerRefusesAnExcuseOnlyAnUntrackedSiblingCarries(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "isolated.toml"), `
name = "isolated"
description = "A package that touches HOME declares its isolation"
severity = "deny"

[scope]
include = ["pkg/*.txt"]

[matcher]
kind = "marker-in-package"
trigger = "DANGER"
marker = "ISOLATED"
`)
	mustWrite(t, filepath.Join(root, "pkg", "keep.txt"), "quiet\n")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "pkg", "risky.txt"), "DANGER\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "pkg", "main.txt"), "ISOLATED\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "pkg/risky.txt:1") {
		t.Fatalf("the only sibling carrying the marker is untracked: %+v", res)
	}
}

// A go-bench-ceiling record is a committed file: a staged regression hidden
// by an unstaged re-record still lands.
func TestRatchetStage_BenchCeilingJudgesTheStagedRecord(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "bench.toml"), `
name = "bench"
description = "Allocation ceilings only come down"
severity = "deny"
baseline = ".ratchet/baselines/bench.txt"

[scope]
include = ["bench.txt"]

[matcher]
kind = "go-bench-ceiling"
files = "bench.txt"
`)
	record := func(bytes string) string {
		return "BenchmarkX-8   100   50 ns/op   " + bytes + " B/op   1 allocs/op\n"
	}
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "bench.txt"),
		"bench.txt|BenchmarkX|B/op | 10\nbench.txt|BenchmarkX|allocs/op | 1\n")
	mustWrite(t, filepath.Join(root, "bench.txt"), record("10"))
	gitAddAll(t, root)
	commitAll(t, root)

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("the committed record sits at its ceiling: %s", res.Message)
	}
	mustWrite(t, filepath.Join(root, "bench.txt"), record("20"))
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "bench.txt"), record("10"))

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "B/op") {
		t.Fatalf("the staged record raises B/op from 10 to 20: %+v", res)
	}
}

// A json-number-ceiling over a committed JSON file (not build output under
// target/) is judged on the staged file too.
func TestRatchetStage_JSONCeilingJudgesTheStagedFile(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "perf.toml"), `
name = "perf"
description = "Recorded latency only comes down"
severity = "deny"
baseline = ".ratchet/baselines/perf.txt"

[scope]
include = ["perf/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "perf/*.json"
path = "ms"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "perf.txt"), "perf | 10\n")
	mustWrite(t, filepath.Join(root, "perf", "x.json"), `{"ms": 10}`+"\n")
	gitAddAll(t, root)
	commitAll(t, root)

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("the committed value sits at its ceiling: %s", res.Message)
	}
	mustWrite(t, filepath.Join(root, "perf", "x.json"), `{"ms": 20}`+"\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "perf", "x.json"), `{"ms": 10}`+"\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "ms = 20") {
		t.Fatalf("the staged value raises ms from 10 to 20: %+v", res)
	}
}

// A containment waiver lives in the superset file, so it is read from the
// staged superset too: an unstaged waiver waives nothing this commit lands.
func TestRatchetStage_ContainmentRefusesAGapWaivedOnlyOnDisk(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "contained.toml"), `
name = "contained"
description = "The stand-in refuses at least what the real system refuses"
severity = "deny"
escape = "// substitute-ok:"
baseline = ".ratchet/baselines/contained.txt"

[scope]
include = ["real.txt", "standin.txt"]

[matcher]
kind = "file-set-containment"
superset_file = "real.txt"
subset_file = "standin.txt"
capture = "item ([a-z]+)"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "contained.txt"), "")
	mustWrite(t, filepath.Join(root, "real.txt"), "item a\n")
	mustWrite(t, filepath.Join(root, "standin.txt"), "item a\n")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "standin.txt"), "item a\nitem b\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "real.txt"), "item a\n// substitute-ok: b is refused upstream\n")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, `missing "b"`) {
		t.Fatalf("the staged superset carries no waiver: %+v", res)
	}
}

// An untracked bench record is in no commit, so it measures nothing: its
// benchmark has no baseline row and would read as a fresh regression.
func TestRatchetStage_BenchCeilingIgnoresAnUntrackedRecord(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "bench.toml"), `
name = "bench"
description = "Allocation ceilings only come down"
severity = "deny"
baseline = ".ratchet/baselines/bench.txt"

[scope]
include = ["bench/*.txt"]

[matcher]
kind = "go-bench-ceiling"
files = "bench/*.txt"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "bench.txt"),
		"bench/a.txt|BenchmarkX|B/op | 10\nbench/a.txt|BenchmarkX|allocs/op | 1\n")
	mustWrite(t, filepath.Join(root, "bench", "a.txt"), "BenchmarkX-8   100   50 ns/op   10 B/op   1 allocs/op\n")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "bench", "b.txt"), "BenchmarkY-8   100   50 ns/op   99 B/op   9 allocs/op\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an untracked record is not part of the commit: %s", res.Message)
	}
}

// The same for a JSON ceiling over the repo itself.
func TestRatchetStage_JSONCeilingIgnoresAnUntrackedFile(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "perf.toml"), `
name = "perf"
description = "Recorded latency only comes down"
severity = "deny"
baseline = ".ratchet/baselines/perf.txt"

[scope]
include = ["perf/**/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "perf/**/*.json"
path = "ms"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "perf.txt"), "perf/a | 10\n")
	mustWrite(t, filepath.Join(root, "perf", "a", "x.json"), `{"ms": 10}`+"\n")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "perf", "b", "x.json"), `{"ms": 99}`+"\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an untracked JSON file is not part of the commit: %s", res.Message)
	}
}
