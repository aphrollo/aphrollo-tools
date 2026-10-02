package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// nestedLinkedLane prepares a lane whose frontend/node_modules is a real link
// to an install elsewhere (a symlink on Unix, a directory junction made by
// mklink /J on Windows), the shape a consuming repo's lane carries (#1083),
// and returns the package file reached through it.
func nestedLinkedLane(t *testing.T) (repo, wt, branch, target string) {
	t.Helper()
	repo, wt, branch = preparedRepo(t)
	ignoreNodeModules(t, repo)
	install := filepath.Join(t.TempDir(), depinstall.NodeModules)
	target = installedPackage(t, filepath.Dir(install))
	if err := os.MkdirAll(filepath.Join(wt, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := depinstall.LinkDir(install, filepath.Join(wt, "frontend", depinstall.NodeModules)); err != nil {
		t.Fatal(err)
	}
	return repo, wt, branch, target
}

func TestPruneTicket_AFrontendNodeModulesLinkIsUnlinkedAndItsTargetKept(t *testing.T) {
	repo, wt, branch, target := nestedLinkedLane(t)
	p, err := PruneTicketPlan(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Force = true
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the lane must be gone, lstat err = %v", err)
	}
	mustExist(t, target, "the link's target")
}

func TestRemove_AFrontendNodeModulesLinkIsUnlinkedAndItsTargetKept(t *testing.T) {
	repo, wt, branch, target := nestedLinkedLane(t)
	r, err := RemovePlan(repo, branch, "")
	if err != nil {
		t.Fatal(err)
	}
	r.Force = true
	var out, errb bytes.Buffer
	if err := r.Run(&out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the lane must be gone, lstat err = %v", err)
	}
	mustExist(t, target, "the link's target")
}

func TestPrune_ASweepUnlinksAFrontendNodeModulesLinkAndKeepsItsTarget(t *testing.T) {
	repo, wt, branch, target := nestedLinkedLane(t)
	stubPRState(t, func(_, b string) (string, error) {
		if b == branch {
			return "MERGED", nil
		}
		return "", nil
	})
	head := headSHA(t, wt)
	stubPRHeadOid(t, func(_, b string) (string, error) {
		if b == branch {
			return head, nil
		}
		return "", nil
	})
	p, err := PrunePlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := p.Run(true, &out, &errb); err != nil {
		t.Fatalf("Run: %v\n%s", err, errb.String())
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the lane must be gone, lstat err = %v\n%s", err, errb.String())
	}
	mustExist(t, target, "the link's target")
}
