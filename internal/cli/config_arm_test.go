package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

// onLane points the repo's HEAD at a branch, the way a checkout of it would.
func (e cfgEnv) onLane(t *testing.T, branch string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.repo, ".git", "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// armRow is the line `config show` prints for the lane's tdd mode.
func armRow(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "lane mode") {
			return line
		}
	}
	return ""
}

func TestConfigShow_NamesTheArmOfTheLaneAndWhyItIsThere(t *testing.T) {
	e := newCfgEnv(t)
	e.onLane(t, "lane/b2")
	arm := tddarm.Of(tddarm.RepoKey(e.repo), "lane/b2")
	_, out, _ := e.run("show", "--dir", e.repo)
	row := armRow(out)
	for _, want := range []string{"lane/b2", arm + " arm", "assigned"} {
		if !strings.Contains(row, want) {
			t.Errorf("lane mode row %q lacks %q\n%s", row, want, out)
		}
	}
}

func TestConfigShow_APinnedTddIsOutsideTheArmsAndSaysWhoPinnedIt(t *testing.T) {
	e := newCfgEnv(t)
	e.onLane(t, "lane/b2")
	e.write(t, "trellis.toml", "tdd = \"off\"\n")
	_, out, _ := e.run("show", "--dir", e.repo)
	row := armRow(out)
	for _, want := range []string{"lane/b2", "off", "pinned", "repo trellis.toml:1", "not in the A/B"} {
		if !strings.Contains(row, want) {
			t.Errorf("lane mode row %q lacks %q\n%s", row, want, out)
		}
	}
}

func TestConfigShow_ACheckoutOnTrunkIsInNoArm(t *testing.T) {
	e := newCfgEnv(t)
	e.onLane(t, "main")
	_, out, _ := e.run("show", "--dir", e.repo)
	if row := armRow(out); !strings.Contains(row, "no lane") || !strings.Contains(row, "no arm") {
		t.Errorf("lane mode row %q, want it to say the checkout has no lane and so no arm\n%s", row, out)
	}
}
