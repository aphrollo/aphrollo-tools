package lawgate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const secretJavaRel = ".ratchet/baselines/secret_java.txt"

const secretJavaLaw = `name         = "secret_java"
description  = "a secret stays out of code"
severity     = "deny"
baseline     = ".ratchet/baselines/secret_java.txt"
mask_strings = true

[scope]
include = ["**/*.java"]

[matcher]
kind    = "regex-absent"
pattern = "SECRET"
key     = "file:line-content-hash"
`

// A Java text block holds SECRET after three quotes: the default lexer reads
// each as opening or closing a string and leaves SECRET visible, the Java row
// masks the whole block.
const secretJavaModule = "class A {\n  String s = \"\"\"\n      a \"b\" \"c SECRET\n      \"\"\";\n}\n"

const secretJavaRow = "app/A.java | a \"b\" \"c SECRET\n"

func javaGuardRepo(t *testing.T, committed, staged string) string {
	t.Helper()
	root := t.TempDir()
	tddtest.GitInit(t, root)
	tddtest.MustWrite(t, filepath.Join(root, ".ratchet", "laws", "secret_java.toml"), secretJavaLaw)
	tddtest.MustWrite(t, filepath.Join(root, "app", "A.java"), secretJavaModule)
	tddtest.MustWrite(t, filepath.Join(root, secretJavaRel), committed)
	tddtest.GitAddAll(t, root)
	tddtest.CommitAll(t, root)
	tddtest.MustWrite(t, filepath.Join(root, secretJavaRel), staged)
	tddtest.GitAddAll(t, root)
	return root
}

// A baseline stamped at view 2 was written before the Java row: moving it to
// view 3 is a migration, admitted when it equals its recomputation.
func TestBaselineGuard_AdmitsARaisedStampThatItsOwnRecomputationReproduces(t *testing.T) {
	for name, committed := range map[string]string{
		"unstamped":    secretJavaRow,
		"stamped at 2": "# scan-view: 2\n" + secretJavaRow,
	} {
		root := javaGuardRepo(t, committed, "# scan-view: 3\n")
		if res := baselineStage("precommit", root); res.Blocked {
			t.Errorf("%s: the recomputed view 3 migration must pass: %s", name, res.Message)
		}
	}
}

// A stamp raised to 3 over a made-up row is a hand-made raise.
func TestBaselineGuard_RefusesARaisedStampCarryingARowTheTreeDoesNotHold(t *testing.T) {
	root := javaGuardRepo(t, "# scan-view: 2\n"+secretJavaRow,
		"# scan-view: 3\n"+secretJavaRow+"app/B.java | made up SECRET\n")
	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "extra row") || !strings.Contains(res.Message, "baseline-rejected") {
		t.Fatalf("a stamp at 3 over a row the recomputation lacks must be refused: %+v", res)
	}
}

// A baseline already at view 3 is not a migration: the ordinary raise rules
// judge it, and a row added to it is a raise.
func TestBaselineGuard_ATextBlockRowAddedToAViewThreeBaselineIsARaise(t *testing.T) {
	root := javaGuardRepo(t, "# scan-view: 3\n", "# scan-view: 3\n"+secretJavaRow)
	if res := baselineStage("precommit", root); !res.Blocked {
		t.Fatal("a row added under an unchanged stamp must be refused as a raise")
	}
}
