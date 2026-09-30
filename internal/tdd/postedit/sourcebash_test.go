package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceBashRepo is a gate-managed git repo with a subdirectory.
func sourceBashRepo(t *testing.T) string {
	t.Helper()
	dir := prRepo(t, "")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sourceBashBlocked(t *testing.T, cwd, cmd string) Decision {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return SourceBashDecision(bashPayload(t, "s-source-bash", cwd, cmd))
}

func TestSourceBashDecision_RefusesEachShapeThatWritesASourceFile(t *testing.T) {
	dir := sourceBashRepo(t)
	for cmd, want := range map[string]string{
		"cat > x.go <<'EOF'\npackage x\nEOF":       "x.go",
		"echo hi >> internal/a.rs":                 "internal/a.rs",
		"sed -i 's/a/b/' x.ts":                     "x.ts",
		"echo a | tee x.py":                        "x.py",
		"cp a.go b.go":                             "b.go",
		"mv tmp x.tsx":                             "x.tsx",
		"install -m 644 t.svelte x.svelte":         "x.svelte",
		"echo a > x_test.go":                       "x_test.go",
		"cd sub && echo a > x.vue":                 "sub/x.vue",
		`python3 -c "open('x.go','w').write('a')"`: "x.go",
		`FOO=1 python3.12 -c "open('x.go','a')"`:   "x.go",
		"python3 - <<'EOF'\nfrom pathlib import Path\nPath(\"x.go\").write_text(\"p\")\nEOF": "x.go",
		"python - <<EOF\nwith open(\"a/b.py\", \"w\") as f:\n    f.write(\"x\")\nEOF":        "a/b.py",
		"python3 - <<'EOF'\np = \"x.go\"\nopen(p, \"w\").write(\"a\")\nEOF":                  "x.go",
		`node -e "require('fs').writeFileSync('x.ts','a')"`:                                  "x.ts",
		`ruby -e "File.write('x.rb','a')"`:                                                   "x.rb",
		"perl -pi -e 's/a/b/' x.go":                                                          "x.go",
		`bash -c "echo a > x.go"`:                                                            "x.go",
		"bash <<EOF\ncat > x.go\nEOF":                                                        "x.go",
		"echo $(echo a > x.go)":                                                              "x.go",
	} {
		d := sourceBashBlocked(t, dir, cmd)
		if d.Action != Block || d.Policy != sourceBashPolicy {
			t.Errorf("%q: got %+v, want Block/%s", cmd, d, sourceBashPolicy)
			continue
		}
		wantReason := "write " + want + " with Edit/Write, not Bash — the per-edit gate (format, laws, tests) only runs on those"
		if d.Reason != wantReason {
			t.Errorf("%q: reason %q, want %q", cmd, d.Reason, wantReason)
		}
	}
}

func TestSourceBashDecision_AllowsWhatIsNotASourceWrite(t *testing.T) {
	dir := sourceBashRepo(t)
	for _, cmd := range []string{
		"cat x.go",
		"grep foo x.go > /tmp/sourcebash-out.txt",
		"echo a > out.log",
		"echo a > notes.md",
		"echo a > conf.toml",
		"echo a > data.json",
		"echo a > testdata/a.go",
		"cp x.go /tmp/sourcebash-x.go",
		"go run ./tools/tddsplit -regen",
		"gofmt -w x.go",
		"goimports -w .",
		"go generate ./...",
		"aphrollo refactor rename A B --apply",
		`git commit -m "write x.go"`,
		"git mv a.go b.go",
		`echo "x.go" > notes.txt`,
		`python3 -c "print(open('x.go').read())"`,
		`python3 -c "open('out.json','w').write('a')"`,
		`python3 -c "open('x.go','r').read()"`,
		"python3 - <<'EOF'\nopen(out, \"w\").write(\"a\")\nEOF",
		"python3 -c 'print(1)' x.go",
		"perl -e 'print 1' x.go",
		"perl -Mstrict -e 'print 1' x.go",
		"python3 -i x.go",
		"echo a > ..x.json",
		"",
	} {
		if d := sourceBashBlocked(t, dir, cmd); d.Action == Block {
			t.Errorf("%q: refused (%s), want allowed", cmd, d.Reason)
		}
	}
}

func TestSourceBashDecision_IgnoresOtherToolsAndBadPayloads(t *testing.T) {
	dir := sourceBashRepo(t)
	if d := SourceBashDecision([]byte("{")); d.Action == Block {
		t.Error("malformed payload must not be refused")
	}
	edit := editPayload(t, "Edit", filepath.Join(dir, "x.go"), "s")
	if d := SourceBashDecision(edit); d.Action == Block {
		t.Error("an Edit call is not a shell write")
	}
	ps := strings.Replace(string(bashPayload(t, "s", dir, "Set-Content -Path x.go -Value a")), `"Bash"`, `"PowerShell"`, 1)
	if d := SourceBashDecision([]byte(ps)); d.Action != Block {
		t.Errorf("PowerShell Set-Content of a source file: got %+v, want Block", d)
	}
}

func TestSourceBashDecision_AllowsOutsideAManagedRepo(t *testing.T) {
	plain := t.TempDir()
	gitInit(t, plain)
	if d := sourceBashBlocked(t, plain, "echo a > x.go"); d.Action == Block {
		t.Error("a repo with no gate marks is not managed")
	}
	if d := sourceBashBlocked(t, t.TempDir(), "echo a > x.go"); d.Action == Block {
		t.Error("a directory outside any repo is not managed")
	}
}

func TestGateManagedRepo_EachMarkAloneMakesARepoManaged(t *testing.T) {
	bare := t.TempDir()
	if gateManagedRepo(bare) {
		t.Fatal("an empty dir is not managed")
	}
	toml := t.TempDir()
	mustWrite(t, filepath.Join(toml, "aphrollo.toml"), "")
	ratchet := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ratchet, ".ratchet"), 0o755); err != nil {
		t.Fatal(err)
	}
	block := t.TempDir()
	mustWrite(t, filepath.Join(block, "CLAUDE.md"), "x\n<!-- aphrollo:begin -->\n")
	plainDoc := t.TempDir()
	mustWrite(t, filepath.Join(plainDoc, "CLAUDE.md"), "no marks\n")
	for name, dir := range map[string]string{"aphrollo.toml": toml, ".ratchet": ratchet, "CLAUDE.md block": block} {
		if !gateManagedRepo(dir) {
			t.Errorf("%s alone must mark a repo managed", name)
		}
	}
	if gateManagedRepo(plainDoc) {
		t.Error("a CLAUDE.md without the block is not a mark")
	}
}

func TestSourceBashDecision_WaiverAllowsAndIsLogged(t *testing.T) {
	dir := sourceBashRepo(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	payload := bashPayload(t, "s-waive", dir, "echo a > x.go")
	if d := SourceBashDecision(payload); d.Action != Block {
		t.Fatalf("before the waiver: %+v, want Block", d)
	}
	if err := setWaiver("s-waive", WallSourceBash, true); err != nil {
		t.Fatal(err)
	}
	if d := SourceBashDecision(payload); d.Action == Block {
		t.Fatal("a waived session must pass")
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil || !strings.Contains(string(log), "override-source-bash-used") {
		t.Fatalf("the waived write is not logged: %v %q", err, log)
	}
	if d := SourceBashDecision(bashPayload(t, "other", dir, "echo a > x.go")); d.Action != Block {
		t.Fatal("the waiver is per session")
	}
}

func TestFirstSourceWrite_DepthBoundIsExact(t *testing.T) {
	dir := sourceBashRepo(t)
	nested := `bash -c 'bash -c "cat > a.go"'`
	if got := firstSourceWrite(nested, dir, maxSourceBashDepth-2); got != "a.go" {
		t.Errorf("one level inside the bound: got %q, want a.go", got)
	}
	if got := firstSourceWrite(nested, dir, maxSourceBashDepth-1); got != "" {
		t.Errorf("one level past the bound: got %q, want none", got)
	}
	if got := firstSourceWrite(`bash -c "cat > a.go"`, dir, maxSourceBashDepth-1); got != "a.go" {
		t.Errorf("the last level still reads redirects: got %q, want a.go", got)
	}
}

func TestScriptSourceWrite_OpenModeBoundary(t *testing.T) {
	dir := sourceBashRepo(t)
	for mode, want := range map[string]string{"w": "x.go", "a": "x.go", "x": "x.go", "r+": "x.go", "wb": "x.go", "r": "", "rb": ""} {
		if got := scriptSourceWrite(`open("x.go", "`+mode+`")`, dir); got != want {
			t.Errorf("mode %q: got %q, want %q", mode, got, want)
		}
	}
	if got := scriptSourceWrite(`open("x.go", mode="w")`, dir); got != "x.go" {
		t.Errorf("mode= keyword: got %q", got)
	}
}

func TestScriptSourceWrite_VariablePathNeedsASourceLiteral(t *testing.T) {
	dir := sourceBashRepo(t)
	if got := scriptSourceWrite("open(out, 'w')", dir); got != "" {
		t.Errorf("variable path, no source literal: got %q, want none", got)
	}
	if got := scriptSourceWrite("p = 'x.go'\nopen(p, 'w')", dir); got != "x.go" {
		t.Errorf("variable path with a source literal: got %q, want x.go", got)
	}
	if got := scriptSourceWrite("open('out.txt', 'w')\nx = 'a.go'", dir); got != "" {
		t.Errorf("literal non-source write must not fall back to other literals: got %q", got)
	}
	if got := scriptSourceWrite("f'{p}.go' open(f'{d}/x.go', 'w')", dir); got != "" {
		t.Errorf("an interpolated path is not a literal: got %q", got)
	}
}

func TestManagedSourceRel_BoundaryCases(t *testing.T) {
	dir := sourceBashRepo(t)
	if got := managedSourceRel(filepath.Join(dir, "..x.go")); got != "..x.go" {
		t.Errorf("a file named ..x.go sits inside the repo: got %q", got)
	}
	if got := managedSourceRel(filepath.Join(filepath.Dir(dir), "x.go")); got != "" {
		t.Errorf("a file above the repo root: got %q, want none", got)
	}
	if got := managedSourceRel(""); got != "" {
		t.Errorf("empty path: got %q", got)
	}
	if got := managedSourceRel(filepath.Join(dir, "x.gox")); got != "" {
		t.Errorf("x.gox is not a source extension: got %q", got)
	}
	if got := managedSourceRel(filepath.Join(dir, "x.GO")); got != "x.GO" {
		t.Errorf("extension match is case-insensitive: got %q", got)
	}
	if got := managedSourceRel(filepath.Join(dir, "go.mod")); got != "" {
		t.Errorf("a manifest is not a judged language: got %q", got)
	}
}

func TestHasInPlaceEditFlag_Boundary(t *testing.T) {
	for _, tc := range []struct {
		prog string
		args []string
		want bool
	}{
		{"perl", []string{"-i"}, true},
		{"perl", []string{"-pi"}, true},
		{"perl", []string{"-i.bak"}, true},
		{"ruby", []string{"-i"}, true},
		{"perl", []string{"-Mstrict"}, false},
		{"perl", []string{"-e", "print"}, false},
		{"perl", []string{"x.go"}, false},
		{"python", []string{"-i"}, false},
		{"node", []string{"-i"}, false},
	} {
		if got := hasInPlaceEditFlag(tc.prog, tc.args); got != tc.want {
			t.Errorf("%s %v: got %v, want %v", tc.prog, tc.args, got, tc.want)
		}
	}
}

func TestProgramOf_SkipsEnvAndVersion(t *testing.T) {
	for _, tc := range []struct {
		words []string
		prog  string
		n     int
	}{
		{[]string{"python3.12", "-c", "x"}, "python", 2},
		{[]string{"A=1", "B=2", "/usr/bin/node", "s.js"}, "node", 1},
		{[]string{"A=1"}, "", 0},
		{nil, "", 0},
	} {
		prog, args := programOf(tc.words)
		if prog != tc.prog || len(args) != tc.n {
			t.Errorf("%v: got %q %v, want %q and %d args", tc.words, prog, args, tc.prog, tc.n)
		}
	}
}
