package depinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInstall(t *testing.T) {
	mk := func(files ...string) string {
		d := t.TempDir()
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(d, f), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	cases := []struct {
		name      string
		files     []string
		wantOK    bool
		wantFirst string // expected argv[0]
	}{
		{name: "pnpm", files: []string{"pnpm-lock.yaml", "package.json"}, wantOK: true, wantFirst: "pnpm"},
		{name: "yarn", files: []string{"yarn.lock"}, wantOK: true, wantFirst: "yarn"},
		{name: "npm-ci", files: []string{"package-lock.json"}, wantOK: true, wantFirst: "npm"},
		{name: "npm-bare", files: []string{"package.json"}, wantOK: true, wantFirst: "npm"},
		{name: "go", files: []string{"go.mod"}, wantOK: true, wantFirst: "go"},
		{name: "none", files: []string{"README.md"}, wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, ok := Detect(mk(c.files...))
			if ok != c.wantOK {
				t.Fatalf("Detect ok = %v, want %v", ok, c.wantOK)
			}
			if ok && rule.Argv[0] != c.wantFirst {
				t.Fatalf("Detect argv[0] = %q, want %q", rule.Argv[0], c.wantFirst)
			}
		})
	}
}

// pnpm-lock must win over a co-located package.json (priority order).
func TestDetectInstall_Priority(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"package.json", "package-lock.json", "pnpm-lock.yaml"} {
		os.WriteFile(filepath.Join(d, f), []byte("{}"), 0o644)
	}
	rule, ok := Detect(d)
	if !ok || rule.Argv[0] != "pnpm" {
		t.Fatalf("priority: got ok=%v argv=%v, want pnpm", ok, rule.Argv)
	}
}
