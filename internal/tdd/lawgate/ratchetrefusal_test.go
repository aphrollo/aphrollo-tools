package lawgate

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

func TestRefusalDetail_NamesEachRefusedLawAndFileOnceCappedAtTenWithTheCount(t *testing.T) {
	var findings []ratchet.Finding
	findings = append(findings,
		ratchet.Finding{Law: "warned", Severity: "warn", File: "w.go"},
		ratchet.Finding{Law: "size", Severity: "deny", File: "a.go", Files: []ratchet.FileCount{{File: "a.go"}, {File: "b.go"}}},
		ratchet.Finding{Law: "size", Severity: "deny", File: "a.go"},
		ratchet.Finding{Law: "graph", Severity: "deny"},
	)
	got := refusalDetail(findings)
	if got["hits"] != "graph|;size|a.go;size|b.go" || got["hit_count"] != "3" {
		t.Fatalf("detail = %v, want 3 distinct deny pairs, sorted, the warn finding left out", got)
	}

	findings = nil
	for i := range 14 {
		findings = append(findings, ratchet.Finding{Law: "size", Severity: "deny", File: fmt.Sprintf("f%02d.go", i)})
	}
	got = refusalDetail(findings)
	if n := strings.Count(got["hits"], ";") + 1; n != 10 || got["hit_count"] != "14" {
		t.Fatalf("recorded %d pairs, count %q, want 10 recorded of 14", n, got["hit_count"])
	}
}

func TestRefusalDetail_AFindingWithNoDenyIsNoDetail(t *testing.T) {
	if got := refusalDetail([]ratchet.Finding{{Law: "w", Severity: "warn", File: "a"}}); got != nil {
		t.Fatalf("detail = %v, want none", got)
	}
}

func TestRatchetStage_ARefusalEventNamesTheLawAndFileItRefused(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := lawTree(t, "deny")
	addFixtures(t, root)
	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	if res := ratchetStage("precommit", root); !res.Blocked {
		t.Fatal("the regression was not refused")
	}
	for _, e := range ReadEvents(root) {
		if e.Kind == "commit_gate" && e.Verdict == "ratchet-rejected" {
			if e.Detail["hits"] != "nan-guard|crates/a/src/lib.rs" || e.Detail["hit_count"] != "1" {
				t.Fatalf("detail = %v, want the one refused law and file", e.Detail)
			}
			return
		}
	}
	t.Fatal("no ratchet-rejected commit_gate event was recorded")
}
