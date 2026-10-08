package mutation

import (
	"strings"
	"testing"
)

const skipSample = `package p

import (
	rd "crypto/rand"
	"math/rand"
)

func fill(b []byte) error {
	if _, err := rd.Read(b); err != nil {
		return err
	}
	if n := rand.Intn(3); n > 1 {
		return nil
	}
	return nil
}
`

func skipLines(lines ...int) map[int]bool {
	m := map[int]bool{}
	for _, l := range lines {
		m[l] = true
	}
	return m
}

func skipNames(ms []commitMutant) []string {
	var out []string
	for _, m := range ms {
		out = append(out, mutantLineOf(m.File, m.Line, m.Col, m.Mutation))
	}
	return out
}

func TestEnumerateMutants_SkipsTheErrorTestOfACallThatCannotFail(t *testing.T) {
	all := enumerateMutants("p/p.go", []byte(skipSample), skipLines(9, 10, 12), defaultMutantsSkip)

	var skipped, kept []commitMutant
	for _, m := range all {
		if m.Skipped {
			skipped = append(skipped, m)
		} else {
			kept = append(kept, m)
		}
	}
	// The error test of the aliased crypto/rand.Read is skipped; the one of
	// math/rand.Intn, which is not on the list, is measured.
	if got := strings.Join(skipNames(skipped), ","); got != "p/p.go:9:31: CONDITIONALS_NEGATION" {
		t.Errorf("skipped = %q, want the error test of rd.Read alone", got)
	}
	if got := strings.Join(skipNames(kept), ","); got != "p/p.go:12:26: CONDITIONALS_BOUNDARY,p/p.go:12:26: CONDITIONALS_NEGATION" {
		t.Errorf("kept = %q, want both mutants of n > 1", got)
	}
	if none := enumerateMutants("p/p.go", []byte(skipSample), skipLines(9), nil); len(none) != 1 || none[0].Skipped {
		t.Errorf("with an empty list = %+v, want the mutant measured", none)
	}
}

func TestEnumerateMutants_ABodyMutantOfASkippedIfIsStillMeasured(t *testing.T) {
	src := "package p\n\nimport \"crypto/rand\"\n\nfunc f(b []byte) int {\n\tif _, err := rand.Read(b); err != nil {\n\t\treturn 1 + 2\n\t}\n\treturn 0\n}\n"

	got := enumerateMutants("p/p.go", []byte(src), skipLines(6, 7), defaultMutantsSkip)

	var kept []string
	for _, m := range got {
		if !m.Skipped {
			kept = append(kept, mutantLineOf(m.File, m.Line, m.Col, m.Mutation))
		}
	}
	if len(kept) != 1 || kept[0] != "p/p.go:7:12: ARITHMETIC_BASE" {
		t.Errorf("kept = %v, want the arithmetic in the error branch measured", kept)
	}
}

func TestMutantsConfig_SkipListIsTheDefaultPlusTheRepos(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-skip = [\"example.com/x/y.Do\"]\n")

	cfg, err := ReadMutantsConfig(root)

	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.SkipList(), " "); got != "crypto/rand.Read example.com/x/y.Do" {
		t.Errorf("skip list = %q", got)
	}
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-skip = [\"nodot\"]\n")
	if _, err := ReadMutantsConfig(root); err == nil || !strings.Contains(err.Error(), `"nodot"`) {
		t.Errorf("a malformed entry was accepted or not named: %v", err)
	}
}

func TestJudgeMutants_CountsTheSkippedAndNeverRefusesOnThem(t *testing.T) {
	v := judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "a.go", Line: 1, Col: 1, Mutation: "CONDITIONALS_NEGATION", Status: mutantSkipped, Note: "mutants-skip"},
		{File: "a.go", Line: 2, Col: 1, Mutation: "ARITHMETIC_BASE", Status: "caught"},
	})

	if v.Refused || v.Unviable != 0 || v.SkipListed != 1 || !strings.Contains(v.Message, ", 1 skipped by mutants-skip") {
		t.Errorf("verdict = %+v\n%s", v, v.Message)
	}
}

func TestMarkSkipped_TurnsGremlinsOutcomesAtSkippedPositionsIntoCountedSkips(t *testing.T) {
	root := t.TempDir()
	write(t, root, "p/p.go", skipSample)
	in := []MutantOutcome{
		{File: "p/p.go", Line: 9, Col: 31, Mutation: "CONDITIONALS_NEGATION", Status: "missed"},
		{File: "p/p.go", Line: 12, Col: 26, Mutation: "CONDITIONALS_NEGATION", Status: "missed"},
	}

	out := markSkipped(root, in, defaultMutantsSkip)

	if out[0].Status != mutantSkipped || out[1].Status != "missed" {
		t.Errorf("statuses = %q, %q, want the call that cannot fail skipped and the other measured", out[0].Status, out[1].Status)
	}
}

func skipKept(t *testing.T, src string, lines ...int) (kept, skipped []string) {
	t.Helper()
	for _, m := range enumerateMutants("p/p.go", []byte(src), skipLines(lines...), defaultMutantsSkip) {
		name := mutantLineOf(m.File, m.Line, m.Col, m.Mutation)
		if m.Skipped {
			skipped = append(skipped, name)
		} else {
			kept = append(kept, name)
		}
	}
	return kept, skipped
}

// Only the comparison on the call's error result is skipped: a second operand
// of the condition is measured like any other.
func TestEnumerateMutants_OnlyTheErrorComparisonIsSkipped(t *testing.T) {
	src := "package p\n\nimport \"crypto/rand\"\n\nfunc f(b []byte) bool {\n\tif _, err := rand.Read(b); err != nil || len(b) > 4 {\n\t\treturn true\n\t}\n\treturn false\n}\n"

	kept, skipped := skipKept(t, src, 6)

	if got := strings.Join(skipped, ","); got != "p/p.go:6:33: CONDITIONALS_NEGATION" {
		t.Errorf("skipped = %q, want the err != nil operator alone", got)
	}
	if got := strings.Join(kept, ","); got != "p/p.go:6:50: CONDITIONALS_BOUNDARY,p/p.go:6:50: CONDITIONALS_NEGATION" {
		t.Errorf("kept = %q, want both mutants of len(b) > 4", got)
	}
}

// The error may be assigned on the statement before the if, and err == nil is
// the same comparison.
func TestEnumerateMutants_TheErrorAssignedJustBeforeTheIfIsSkipped(t *testing.T) {
	src := "package p\n\nimport \"crypto/rand\"\n\nfunc f(b []byte) bool {\n\t_, err := rand.Read(b)\n\tif err == nil && len(b) > 4 {\n\t\treturn true\n\t}\n\treturn false\n}\n"

	kept, skipped := skipKept(t, src, 7)

	if got := strings.Join(skipped, ","); got != "p/p.go:7:9: CONDITIONALS_NEGATION" {
		t.Errorf("skipped = %q, want err == nil alone", got)
	}
	if len(kept) != 2 {
		t.Errorf("kept = %v, want the two mutants of len(b) > 4", kept)
	}
}

// A function literal in the condition is its own code: nothing in it is skipped.
func TestEnumerateMutants_NothingInAFunctionLiteralIsSkipped(t *testing.T) {
	src := "package p\n\nimport \"crypto/rand\"\n\nfunc f(b []byte) bool {\n\tif _, err := rand.Read(b); err != nil || func() bool { return err != nil }() {\n\t\treturn true\n\t}\n\treturn false\n}\n"

	_, skipped := skipKept(t, src, 6)

	if len(skipped) != 1 || !strings.HasPrefix(skipped[0], "p/p.go:6:33:") {
		t.Errorf("skipped = %v, want the outer err != nil alone", skipped)
	}
}

// A name that is a local variable, a parameter or a receiver in the function is
// not the import, whatever it is called.
func TestEnumerateMutants_ALocalNamedLikeTheImportIsNotTheImport(t *testing.T) {
	for name, src := range map[string]string{
		"a parameter": "package p\n\nimport \"crypto/rand\"\n\ntype R struct{}\n\nfunc (R) Read([]byte) (int, error) { return 0, nil }\n\nfunc f(rand R, b []byte) bool {\n\tif _, err := rand.Read(b); err != nil {\n\t\treturn true\n\t}\n\treturn false\n}\n",
		"a local":     "package p\n\nimport \"crypto/rand\"\n\ntype R struct{}\n\nfunc (R) Read([]byte) (int, error) { return 0, nil }\n\nfunc f(b []byte) bool {\n\trand := R{}\n\tif _, err := rand.Read(b); err != nil {\n\t\treturn true\n\t}\n\treturn false\n}\n",
		"a receiver":  "package p\n\nimport \"crypto/rand\"\n\ntype R struct{}\n\nfunc (R) Read([]byte) (int, error) { return 0, nil }\n\nfunc (rand R) f(b []byte) bool {\n\tif _, err := rand.Read(b); err != nil {\n\t\treturn true\n\t}\n\treturn false\n}\n",
		"a range var": "package p\n\nimport \"crypto/rand\"\n\ntype R struct{}\n\nfunc (R) Read([]byte) (int, error) { return 0, nil }\n\nfunc f(rs []R, b []byte) bool {\n\tfor _, rand := range rs {\n\t\tif _, err := rand.Read(b); err != nil {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}\n",
	} {
		all := enumerateMutants("p/p.go", []byte(src), skipLines(10, 11, 12), defaultMutantsSkip)
		if len(all) == 0 {
			t.Fatalf("%s: no mutants found", name)
		}
		for _, m := range all {
			if m.Skipped {
				t.Errorf("%s: %s was skipped, but %q is not the import there", name, mutantLineOf(m.File, m.Line, m.Col, m.Mutation), "rand")
			}
		}
	}
}

// The commit gate says how many mutants the skip list took out, and runs none
// of them.
func TestMutantsAtCommitStage_SaysHowManyMutantsTheSkipListTookOut(t *testing.T) {
	_, root := commitStage(t, "")
	src := strings.Replace(commitBaseSource, "package gate\n", "package gate\n\nimport \"crypto/rand\"\n", 1)
	write(t, root, "gate/gate.go", src+"\nfunc Fill(b []byte) bool {\n\tif _, err := rand.Read(b); err != nil {\n\t\treturn false\n\t}\n\treturn true\n}\n")
	gitDo(t, root, "add", "gate/gate.go")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	stderr := captureStderr(t, func() { mutantsAtCommitStage("precommit", root) })

	if !strings.Contains(stderr, "1 mutant(s) skipped by mutants-skip") {
		t.Errorf("stderr lacks the skipped count:\n%s", stderr)
	}
	if s.count() != 0 {
		t.Errorf("go test ran %d times, want none: the only mutant added is skipped", s.count())
	}
}

// Another equality in the condition, one that is not the call's error against
// nil, is measured.
func TestEnumerateMutants_AnotherEqualityInTheConditionIsMeasured(t *testing.T) {
	src := "package p\n\nimport \"crypto/rand\"\n\nfunc f(b []byte, other error) bool {\n\tif _, err := rand.Read(b); err != nil || len(b) != 4 || other == nil {\n\t\treturn true\n\t}\n\treturn false\n}\n"

	kept, skipped := skipKept(t, src, 6)

	if len(skipped) != 1 || !strings.HasPrefix(skipped[0], "p/p.go:6:33:") {
		t.Errorf("skipped = %v, want the call's err != nil alone", skipped)
	}
	if len(kept) != 2 {
		t.Errorf("kept = %v, want len(b) != 4 and other == nil measured", kept)
	}
}
