package tddarm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/tomlsubset"
)

// The arm is a pure function of the repo's identity and the lane's name, so every
// session and every box puts a lane in the same arm. These are pinned values: a change
// to the hash would move lanes between arms in the middle of the experiment.
func TestOf_PutsALaneInTheSameArmOnEveryBox(t *testing.T) {
	for _, c := range []struct{ repo, lane, want string }{
		{"github.com/aphrollo/aphrollo-tools", "lane/a1-redgreen-warn", ArmWarn},
		{"github.com/aphrollo/aphrollo-tools", "lane/b2", ArmEnforce},
		{"github.com/aphrollo/aphrollo-tools", "feature/x", ArmEnforce},
		{"github.com/acme/app", "lane/a1-redgreen-warn", ArmEnforce},
		{"github.com/acme/app", "lane/fix-parse", ArmEnforce},
		{"x", "y", ArmEnforce},
	} {
		if got := Of(c.repo, c.lane); got != c.want {
			t.Errorf("Of(%q, %q) = %q, want %q", c.repo, c.lane, got, c.want)
		}
	}
}

// Lanes named in a run (lane/a1, lane/a2, ...) must not all land in one arm.
func TestOf_SplitsConsecutiveLaneNamesAboutEvenly(t *testing.T) {
	enforce := 0
	const n = 2000
	for i := range n {
		if Of("github.com/acme/app", fmt.Sprintf("lane/fix-%d", i)) == ArmEnforce {
			enforce++
		}
	}
	if enforce < n*45/100 || enforce > n*55/100 {
		t.Errorf("%d of %d lanes in the enforce arm, want a split within 45%% to 55%%", enforce, n)
	}
}

func setting(layer config.Layer, value string) config.Setting {
	return config.Setting{Key: "tdd", Layer: layer, File: "trellis.toml", Line: 3, Value: tomlsubset.Value{Kind: tomlsubset.String, S: value}}
}

func TestResolve_APinAlwaysWinsAndIsOutsideTheExperiment(t *testing.T) {
	for _, layer := range []config.Layer{config.User, config.Repo, config.Env, config.Flag} {
		m := Resolve(setting(layer, "enforce"), "github.com/acme/app", "lane/a1-redgreen-warn")
		if m.TDD != "enforce" || m.Arm != "" || m.Why != WhyPinned || m.Layer != layer.String() {
			t.Errorf("%s layer: %+v, want the pinned enforce with no arm", layer, m)
		}
	}
	if m := Resolve(setting(config.Repo, "off"), "r", "lane/x"); m.TDD != "off" || m.Arm != "" {
		t.Errorf("a pinned off = %+v, want off with no arm", m)
	}
}

func TestResolve_ALaneOfAnUnpinnedRepoTakesItsAssignedArm(t *testing.T) {
	m := Resolve(setting(config.BuiltIn, "warn"), "github.com/aphrollo/aphrollo-tools", "lane/b2")
	if m.TDD != "enforce" || m.Arm != ArmEnforce || m.Why != WhyAssigned {
		t.Errorf("%+v, want lane/b2 assigned to the enforce arm", m)
	}
	m = Resolve(setting(config.BuiltIn, "warn"), "github.com/aphrollo/aphrollo-tools", "lane/a1-redgreen-warn")
	if m.TDD != "warn" || m.Arm != ArmWarn || m.Why != WhyAssigned {
		t.Errorf("%+v, want lane/a1-redgreen-warn assigned to the warn arm", m)
	}
	// A value a layer declared wrongly reads as the built-in one: no pin.
	bad := setting(config.BuiltIn, "warn")
	bad.Fallback = true
	if m := Resolve(bad, "github.com/aphrollo/aphrollo-tools", "lane/b2"); m.Why != WhyAssigned || m.Arm != ArmEnforce {
		t.Errorf("a rejected declaration = %+v, want the lane still assigned", m)
	}
}

func TestResolve_NoLaneAndTrunkLanesAreInNoArm(t *testing.T) {
	for _, lane := range []string{"", "main", "master", "@trunk"} {
		m := Resolve(setting(config.BuiltIn, "warn"), "r", lane)
		if m.TDD != "warn" || m.Arm != "" || m.Why != WhyNoLane {
			t.Errorf("lane %q: %+v, want the built-in warn in no arm", lane, m)
		}
	}
}

func writeRepo(t *testing.T, dir, remote string) {
	t.Helper()
	cfg := "[core]\n\trepositoryformatversion = 0\n"
	if remote != "" {
		cfg += "[remote \"origin\"]\n\turl = " + remote + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The key is the same wherever the repo is cloned and however it is cloned: it comes
// from the origin remote, not from a path on the box.
func TestRepoKey_IsTheRemoteNotThePathOfTheCloneOnTheBox(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/Aphrollo/Aphrollo-Tools.git",
		"git@github.com:aphrollo/aphrollo-tools.git",
		"ssh://git@github.com/aphrollo/aphrollo-tools",
		"https://user@github.com/aphrollo/aphrollo-tools/",
	} {
		dir := t.TempDir()
		writeRepo(t, dir, remote)
		if got := RepoKey(dir); got != "github.com/aphrollo/aphrollo-tools" {
			t.Errorf("RepoKey with origin %q = %q, want github.com/aphrollo/aphrollo-tools", remote, got)
		}
	}
}

func TestRepoKey_ALinkedWorktreeHasItsMainCheckoutsKeyAndARepoWithNoRemoteItsDirectoryName(t *testing.T) {
	main := filepath.Join(t.TempDir(), "projx")
	writeRepo(t, main, "git@github.com:acme/app.git")
	gitdir := filepath.Join(main, ".git", "worktrees", "lane")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lane := t.TempDir()
	if err := os.WriteFile(filepath.Join(lane, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := RepoKey(lane); got != "github.com/acme/app" {
		t.Errorf("RepoKey(linked worktree) = %q, want the main checkout's github.com/acme/app", got)
	}
	bare := filepath.Join(t.TempDir(), "Scratch")
	writeRepo(t, bare, "")
	if got := RepoKey(bare); got != "scratch" {
		t.Errorf("RepoKey(no remote) = %q, want the directory name scratch", got)
	}
	if got := RepoKey(t.TempDir()); got != "" {
		t.Errorf("RepoKey(no repo) = %q, want empty", got)
	}
}
