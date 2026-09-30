package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

const maxLinesLaw = `
name        = "module_size"
description = "a file stays short"
severity    = "deny"
baseline    = ".ratchet/baselines/module_size.txt"

[scope]
include = ["**/*.py"]

[matcher]
kind = "line-count"
max  = 5
`

// baseRepo is a tree plus the directory standing in for the ref it is judged
// against; files maps a path to the number of bare excepts it holds.
func baseRepo(t *testing.T, baseline string, tree, base map[string]int) (root string, baseDir string) {
	t.Helper()
	root = exceptRepo(t, baseline, tree)
	baseDir = t.TempDir()
	for file, n := range base {
		write(t, filepath.Join(baseDir, file), strings.Repeat("    except Exception:\n", n))
	}
	return root, baseDir
}

func checkAgainst(t *testing.T, root, baseDir string, relative bool) Result {
	t.Helper()
	res, err := Check(Options{Root: root, Base: "main", BaseTree: dirBaseReader{dir: baseDir}, BaseRelative: relative})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res
}

// TestCheck_BaseRelativeCountsOnlyWhatTheTreeAddedSinceTheBase: a tree judged
// against itself has nothing introduced, however far it sits above a
// baseline that never listed its lines; without the option the same run
// reports them all.
func TestCheck_BaseRelativeCountsOnlyWhatTheTreeAddedSinceTheBase(t *testing.T) {
	root, baseDir := baseRepo(t, "", map[string]int{"a.py": 2, "b.py": 3}, map[string]int{"a.py": 2, "b.py": 3})
	if res := checkAgainst(t, root, baseDir, true); len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — the base holds every one of them", res.Findings)
	}
	if res := checkAgainst(t, root, baseDir, false); res.RegressionCount() != 5 {
		t.Fatalf("without BaseRelative RegressionCount = %d, want 5 against an empty baseline", res.RegressionCount())
	}
}

// TestCheck_BaseRelativeReportsTheLinesAddedAboveTheBase: one more than the
// base holds is one regression, judged against the base's own count.
func TestCheck_BaseRelativeReportsTheLinesAddedAboveTheBase(t *testing.T) {
	root, baseDir := baseRepo(t, "", map[string]int{"a.py": 2, "b.py": 4}, map[string]int{"a.py": 2, "b.py": 3})
	res := checkAgainst(t, root, baseDir, true)
	if len(res.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the one text over the base", res.Findings)
	}
	f := res.Findings[0]
	if f.Baseline != 5 || f.Measured != 6 || f.Excess != 1 {
		t.Errorf("baseline/measured/excess = %d/%d/%d, want 5/6/1 — the base's count is the ceiling", f.Baseline, f.Measured, f.Excess)
	}
}

// TestCheck_BaseRelativeKeepsTheHigherOfBaselineAndBase: a baseline above the
// base still forgives up to its own ceiling.
func TestCheck_BaseRelativeKeepsTheHigherOfBaselineAndBase(t *testing.T) {
	baseline := strings.Repeat("a.py | except Exception:\n", 5)
	root, baseDir := baseRepo(t, baseline, map[string]int{"a.py": 5}, map[string]int{"a.py": 3})
	if res := checkAgainst(t, root, baseDir, true); len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — five is the baseline's own ceiling", res.Findings)
	}
	root, baseDir = baseRepo(t, baseline, map[string]int{"a.py": 6}, map[string]int{"a.py": 3})
	if res := checkAgainst(t, root, baseDir, true); len(res.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the sixth over both ceilings", res.Findings)
	}
}

// TestCheck_BaseRelativeIgnoresLineEndings: a Windows worktree with
// core.autocrlf holds the same line with a CR that the base's blob lacks; the
// two are the same site.
func TestCheck_BaseRelativeIgnoresLineEndings(t *testing.T) {
	root := exceptRepo(t, "", nil)
	write(t, filepath.Join(root, "a.py"), "    except Exception:\r\n    except Exception:\r\n")
	baseDir := t.TempDir()
	write(t, filepath.Join(baseDir, "a.py"), "    except Exception:\n    except Exception:\n")
	if res := checkAgainst(t, root, baseDir, true); len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — CRLF and LF are one line", res.Findings)
	}
}

// TestCheck_BaseRelativeForgivesASiteTheBaseAlreadyHas: two baseline rows for a
// text whose files are gone leave a ceiling of two; the one site the tree holds
// is at a path no row names, which the site check reports unless the base has it
// too.
func TestCheck_BaseRelativeForgivesASiteTheBaseAlreadyHas(t *testing.T) {
	baseline := "gone1.py | except Exception:\ngone2.py | except Exception:\n"
	root, baseDir := baseRepo(t, baseline, map[string]int{"a.py": 1}, map[string]int{"a.py": 1})
	if res := checkAgainst(t, root, baseDir, false); len(res.Findings) != 1 {
		t.Fatalf("without BaseRelative Findings = %+v, want the site no row names", res.Findings)
	}
	if res := checkAgainst(t, root, baseDir, true); len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — the base has the site", res.Findings)
	}
}

// TestCheck_BaseRelativeJudgesAFileKeyedLawByTheBaseCount: a file over its
// ceiling at the base is not the change's doing; growing it is.
func TestCheck_BaseRelativeJudgesAFileKeyedLawByTheBaseCount(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "module_size", maxLinesLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "module_size.txt"), "")
	baseDir := t.TempDir()
	write(t, filepath.Join(baseDir, "big.py"), strings.Repeat("x = 1\n", 9))
	write(t, filepath.Join(root, "big.py"), strings.Repeat("x = 1\n", 9))
	if res := checkAgainst(t, root, baseDir, true); len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none — 9 lines at the base and now", res.Findings)
	}
	write(t, filepath.Join(root, "big.py"), strings.Repeat("x = 1\n", 10))
	res := checkAgainst(t, root, baseDir, true)
	if len(res.Findings) != 1 || res.Findings[0].Baseline != 9 || res.Findings[0].Measured != 10 {
		t.Fatalf("Findings = %+v, want big.py at 10 against the base's 9", res.Findings)
	}
}
