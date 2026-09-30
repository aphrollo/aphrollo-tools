package ratchet

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const bareExceptLaw = `
name        = "except_pass_api"
description = "no bare except"
severity    = "deny"
baseline    = ".ratchet/baselines/except_pass_api.txt"

[scope]
include = ["**/*.py"]

[matcher]
kind    = "regex-absent"
pattern = "^\\s*except Exception:$"
key     = "file:line-content-hash"
`

func exceptRepo(t *testing.T, baseline string, perFile map[string]int) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "except_pass_api", bareExceptLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "except_pass_api.txt"), baseline)
	for file, n := range perFile {
		write(t, filepath.Join(root, file), strings.Repeat("    except Exception:\n", n))
	}
	return root
}

// TestCheck_AContentKeyedRegressionReportsItsRealCountAndTheFilesHoldingIt: a
// text-keyed baseline counts one workspace-wide total per offending text, so
// one finding stands for every occurrence above it. It has to say how many
// that is, and where, or the summary plans the work as one line in one
// arbitrary file.
func TestCheck_AContentKeyedRegressionReportsItsRealCountAndTheFilesHoldingIt(t *testing.T) {
	root := exceptRepo(t, "a.py | except Exception:\n", map[string]int{"a.py": 2, "b.py": 3, "c.py": 1})
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want one for the one offending text", res.Findings)
	}
	f := res.Findings[0]
	if f.Baseline != 1 || f.Measured != 6 || f.Excess != 5 {
		t.Errorf("baseline/measured/excess = %d/%d/%d, want 1/6/5", f.Baseline, f.Measured, f.Excess)
	}
	want := []FileCount{{File: "b.py", Count: 3}, {File: "a.py", Count: 1}, {File: "c.py", Count: 1}}
	if !reflect.DeepEqual(f.Files, want) {
		t.Errorf("files = %+v, want %+v — the files holding the excess, most first", f.Files, want)
	}
	if got := res.RegressionCount(); got != 5 {
		t.Errorf("RegressionCount = %d, want 5 lines", got)
	}
	line := res.Lines()[0]
	if !strings.Contains(line, "5 over its baseline in 3 files: b.py 3, a.py 1, c.py 1") {
		t.Errorf("line = %q, want the excess, the file count and the files", line)
	}
}

// TestResult_LinesSayNothingMoreForOneOccurrenceInOneFile: the location on the
// line already says it, so the clause would only cost the remedy its room.
func TestResult_LinesSayNothingMoreForOneOccurrenceInOneFile(t *testing.T) {
	r := Result{Findings: []Finding{{Law: "l", File: "a.py", Line: 3, What: "x", Baseline: 0, Measured: 1, Excess: 1, Files: []FileCount{{"a.py", 1}}}}}
	if got, want := r.Lines()[0], "l: a.py:3 x (baseline 0, now 1)"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// TestResult_LinesNameTheSingularFileOfAMultipleOccurrenceExcess and the
// smallest excess spread over two files each get their clause.
func TestResult_LinesNameTheSingularFileOfAMultipleOccurrenceExcess(t *testing.T) {
	two := Result{Findings: []Finding{{Law: "l", File: "a.py", What: "x", Excess: 2, Files: []FileCount{{"a.py", 2}}}}}
	if line := two.Lines()[0]; !strings.Contains(line, "2 over its baseline in 1 file: a.py 2") {
		t.Errorf("line = %q, want `2 over its baseline in 1 file: a.py 2`", line)
	}
	spread := Result{Findings: []Finding{{Law: "l", File: "a.py", What: "x", Excess: 1, Files: []FileCount{{"a.py", 1}, {"b.py", 1}}}}}
	if line := spread.Lines()[0]; !strings.Contains(line, "1 over its baseline in 2 files: a.py 1, b.py 1") {
		t.Errorf("line = %q, want `1 over its baseline in 2 files: a.py 1, b.py 1`", line)
	}
}

// TestResult_RegressionCountCountsLinesNotFindings: a finding that carries no
// excess (a file-keyed ceiling) is one regression; one that carries an excess
// is that many.
func TestResult_RegressionCountCountsLinesNotFindings(t *testing.T) {
	cases := map[string]struct {
		findings []Finding
		want     int
	}{
		"none":                    {nil, 0},
		"one without an excess":   {[]Finding{{}}, 1},
		"an excess of one":        {[]Finding{{Excess: 1}}, 1},
		"an excess of two":        {[]Finding{{Excess: 2}}, 2},
		"a mix sums its findings": {[]Finding{{}, {Excess: 4}, {Excess: 1}}, 6},
	}
	for name, c := range cases {
		if got := (Result{Findings: c.findings}).RegressionCount(); got != c.want {
			t.Errorf("%s: RegressionCount = %d, want %d", name, got, c.want)
		}
	}
}

// TestResult_LinesListAtMostFiveFilesAndSayHowManyMore: the list stays a
// line, and what it leaves out is counted, never dropped silently.
func TestResult_LinesListAtMostFiveFilesAndSayHowManyMore(t *testing.T) {
	for n := 4; n <= 7; n++ {
		var files []FileCount
		for i := 1; i <= n; i++ {
			files = append(files, FileCount{File: fmt.Sprintf("f%d.py", i), Count: 1})
		}
		r := Result{Findings: []Finding{{Law: "l", File: "f1.py", What: "x", Excess: n, Files: files}}}
		line := r.Lines()[0]
		more := ""
		if n > 5 {
			more = fmt.Sprintf(", +%d more", n-5)
		}
		want := fmt.Sprintf("in %d files: f1.py 1, f2.py 1, f3.py 1, f4.py 1", n)
		if n >= 5 {
			want += ", f5.py 1"
		}
		want += more
		if !strings.Contains(line, want) {
			t.Errorf("%d files: line = %q, want it to contain %q", n, line, want)
		}
		if n <= 5 && strings.Contains(line, "more") {
			t.Errorf("%d files: line = %q, want no count of files left out", n, line)
		}
	}
}
