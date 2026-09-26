package depinstall

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// nodeRoot writes files (name -> content) into a fresh directory and, when
// installed is true, a node_modules tree holding one package.
func nodeRoot(t *testing.T, installed bool, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if installed {
		pkg := filepath.Join(d, NodeModules, "fakepkg")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func mustDetect(t *testing.T, root string) Rule {
	t.Helper()
	r, ok := Detect(root)
	if !ok {
		t.Fatalf("no install rule for %s", root)
	}
	return r
}

// The cheap path: the lane already installed exactly the dependency set the
// merged tree pins, so its node_modules serves the merged tree unchanged.
func TestReusable_IdenticalLockfileAndAnInstalledLane(t *testing.T) {
	files := map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": `{"lockfileVersion":3}`}
	lane := nodeRoot(t, true, files)
	merged := nodeRoot(t, false, files)
	if !Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("an identical lockfile and an installed lane must reuse the lane's node_modules")
	}
}

// Trunk moved a dependency: the lane's install is of a different set, and a
// suite run against it would judge packages the merge does not pin.
func TestReusable_ChangedLockfileIsNotReused(t *testing.T) {
	lane := nodeRoot(t, true, map[string]string{"package.json": `{}`, "pnpm-lock.yaml": "a: 1\n"})
	merged := nodeRoot(t, false, map[string]string{"package.json": `{}`, "pnpm-lock.yaml": "a: 2\n"})
	if Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("a lockfile that differs between lane and merged tree must not reuse the lane's node_modules")
	}
}

// Nothing to link to: the lane never installed.
func TestReusable_LaneWithoutNodeModulesIsNotReused(t *testing.T) {
	files := map[string]string{"package.json": `{}`, "yarn.lock": "# yarn\n"}
	lane := nodeRoot(t, false, files)
	merged := nodeRoot(t, false, files)
	if Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("a lane with no node_modules has nothing to reuse")
	}
}

// Without a lockfile, an identical package.json pins nothing: two installs of
// it resolve whatever the registry serves that day.
func TestReusable_NoLockfileIsNeverReused(t *testing.T) {
	files := map[string]string{"package.json": `{"dependencies":{"fakepkg":"^1"}}`}
	lane := nodeRoot(t, true, files)
	merged := nodeRoot(t, false, files)
	if Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("a package.json with no lockfile must not reuse the lane's node_modules")
	}
}

// A lane whose lockfile is missing is not the same install as a merged tree
// that has one, even when the merged file is empty.
func TestReusable_LaneMissingTheLockfileIsNotReused(t *testing.T) {
	lane := nodeRoot(t, true, map[string]string{"package.json": `{}`})
	merged := nodeRoot(t, false, map[string]string{"package.json": `{}`, "package-lock.json": ""})
	if Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("a lockfile the lane lacks is not identical to the merged tree's")
	}
}

// A merged tree without the rule's lockfile is not the lane's install, even
// when the lane's lockfile is empty.
func TestReusable_MergedTreeMissingTheLockfileIsNotReused(t *testing.T) {
	lane := nodeRoot(t, true, map[string]string{"package.json": `{}`, "package-lock.json": ""})
	merged := nodeRoot(t, false, map[string]string{"package.json": `{}`})
	rule := Rule{Marker: "package-lock.json", Argv: []string{"npm", "ci"}, Present: NodeModules}
	if Reusable(rule, lane, merged) {
		t.Fatal("a lockfile the merged tree lacks is not identical to the lane's")
	}
}

// A node_modules that is a file, not a directory, is nothing to link to.
func TestReusable_LaneNodeModulesFileIsNotReused(t *testing.T) {
	files := map[string]string{"package.json": `{}`, "package-lock.json": `{}`, NodeModules: "not a directory"}
	lane := nodeRoot(t, false, files)
	merged := nodeRoot(t, false, map[string]string{"package.json": `{}`, "package-lock.json": `{}`})
	if Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("a node_modules file in the lane has no packages to share")
	}
}

// Only a node rule shares node_modules: a go.mod identical on both sides is
// not a reason to link anything.
func TestReusable_GoRuleIsNeverReused(t *testing.T) {
	files := map[string]string{"go.mod": "module x\n"}
	lane := nodeRoot(t, true, files)
	merged := nodeRoot(t, false, files)
	if Reusable(mustDetect(t, merged), lane, merged) {
		t.Fatal("a go.mod root must not reuse a node_modules")
	}
}

// A link that could not be made is reported, and never recorded for removal.
func TestLinks_MakeReportsALinkThatCouldNotBeMade(t *testing.T) {
	lane := nodeRoot(t, true, map[string]string{"package.json": `{}`})
	checkout := nodeRoot(t, true, map[string]string{"package.json": `{}`})
	var links Links
	if err := links.Make(filepath.Join(lane, NodeModules), filepath.Join(checkout, NodeModules)); err == nil {
		t.Fatal("linking over an existing node_modules must fail")
	}
	if len(links.paths) != 0 {
		t.Fatalf("a failed link was recorded for removal: %v", links.paths)
	}
}

// go.mod has an install rule but no node_modules to share.
func TestIsNode_GoRuleIsNotANodeInstall(t *testing.T) {
	root := nodeRoot(t, false, map[string]string{"go.mod": "module x\n"})
	if mustDetect(t, root).IsNode() {
		t.Fatal("go mod download does not produce node_modules")
	}
	npm := nodeRoot(t, false, map[string]string{"package.json": `{}`})
	if !mustDetect(t, npm).IsNode() {
		t.Fatal("npm install produces node_modules")
	}
}

// The property the merge gate's cleanup rests on: removing a link removes the
// link, and the lane's real node_modules it pointed at keeps every file.
func TestLinks_RemoveDeletesTheLinkNeverItsTarget(t *testing.T) {
	lane := nodeRoot(t, true, map[string]string{"package.json": `{}`})
	checkout := t.TempDir()
	link := filepath.Join(checkout, NodeModules)
	var links Links
	if err := links.Make(filepath.Join(lane, NodeModules), link); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(link, "fakepkg", "index.js")); err != nil {
		t.Fatalf("the link does not reach the lane's packages: %v", err)
	}

	links.Remove()
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the link survived its removal (lstat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(lane, NodeModules, "fakepkg", "index.js")); err != nil {
		t.Fatalf("removing the link deleted through it into the lane's node_modules: %v", err)
	}
}

// Links only ever removes a path as a link. A real directory standing where a
// link was recorded is left whole: Remove never recurses.
func TestLinks_RemoveNeverRecursesIntoARealDirectory(t *testing.T) {
	real := nodeRoot(t, true, map[string]string{"package.json": `{}`})
	links := Links{paths: []string{filepath.Join(real, NodeModules)}}

	links.Remove()

	if _, err := os.Stat(filepath.Join(real, NodeModules, "fakepkg", "index.js")); err != nil {
		t.Fatalf("Remove deleted the contents of a real directory: %v", err)
	}
}

// Windows gets a directory junction, which needs no symlink privilege; the
// command line is built here, where it can be read on any platform.
func TestLinkDir_WindowsMakesAJunction(t *testing.T) {
	origGOOS, origJunction := linkGOOS, junctionFn
	t.Cleanup(func() { linkGOOS, junctionFn = origGOOS, origJunction })
	linkGOOS = "windows"
	var got [][2]string
	junctionFn = func(target, link string) error {
		got = append(got, [2]string{target, link})
		return nil
	}

	link := filepath.Join(t.TempDir(), NodeModules)
	if err := LinkDir(`C:\lane\frontend\node_modules`, link); err != nil {
		t.Fatalf("LinkDir: %v", err)
	}
	want := [][2]string{{`C:\lane\frontend\node_modules`, link}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("junction calls = %v, want %v", got, want)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("a Windows link must be made by the junction, not a symlink (lstat err: %v)", err)
	}
}

// cmd.exe with /s strips exactly the outer quote pair, so each path keeps its
// own quotes and a space in a Windows path survives.
func TestJunctionCmdLine_QuotesBothPaths(t *testing.T) {
	got := junctionCmdLine(`C:\gate prmerge\frontend\node_modules`, `C:\lane dir\frontend\node_modules`)
	want := `cmd.exe /s /c "mklink /J "C:\gate prmerge\frontend\node_modules" "C:\lane dir\frontend\node_modules""`
	if got != want {
		t.Fatalf("junctionCmdLine = %s\nwant %s", got, want)
	}
}
