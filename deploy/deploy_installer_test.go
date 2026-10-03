//go:build linux

package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

// Every package with tests here isolates its git world (internal/gitiso).
func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}

// deploy-prod.sh hands the build to the root-owned installer
// (aphrollo-install-release) when the host grants it, and only falls back to
// writing /opt itself on a host that has not applied the matching
// aphrollo-infra change. These cases run the script in a sandbox with sudo,
// go, goose and rsync stubbed, and check what it staged and what it asked the
// installer to do.

const (
	svc     = "aphrollo-cli"
	binName = "aphrollo"

	stubSudo = `#!/usr/bin/env bash
echo "sudo $*" >> "$OPLOG"
if [ "${1:-}" = "-n" ] && [ "${2:-}" = "-l" ]; then
  [ "${INSTALLER_GRANTED:-1}" = "1" ] && exit 0
  exit 1
fi
if [ "${1:-}" = "-n" ] && [ "${2:-}" = "$APHROLLO_INSTALLER" ]; then
  stage="$APHROLLO_RELEASE_STAGING/aphrollo-cli"
  (cd "$stage" && find . -type f | LC_ALL=C sort) > "$STAGED_LIST"
  (cd "$stage" && sha256sum --check --quiet --strict MANIFEST.sha256) || { echo "manifest-bad" >> "$OPLOG"; exit 9; }
  echo "manifest-ok" >> "$OPLOG"
  exit "${INSTALLER_RC:-0}"
fi
echo "unexpected sudo $*" >> "$OPLOG"
exit 99
`
	stubGoose = `#!/usr/bin/env bash
echo "goose $*" >> "$OPLOG"
`
	stubRsync = `#!/usr/bin/env bash
echo "rsync $*" >> "$OPLOG"
`
	// The script refuses a checkout that is not the newest release tag: the tag
	// lookup and the rev-parse of it are stubbed to agree with GITHUB_SHA.
	stubGit = `#!/usr/bin/env bash
echo "0123456789abcdef0123456789abcdef01234567"
`
	stubSystemctl = `#!/usr/bin/env bash
echo "systemctl $*" >> "$OPLOG"
`
)

type deployRun struct {
	ops      []string
	exitCode int
	output   string
	staged   string // sorted file list of the staging dir at install time
}

func runDeploy(t *testing.T, env map[string]string, preStage func(stage string)) deployRun {
	t.Helper()
	sandbox := t.TempDir()
	stub := filepath.Join(sandbox, "stub")
	repo := filepath.Join(sandbox, "repo")
	stage := filepath.Join(sandbox, "staging", svc)
	deployDir := filepath.Join(sandbox, "deploy")
	for _, d := range []string{stub, filepath.Join(repo, "bin"), filepath.Join(repo, "migrations"), stage, deployDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	for name, body := range map[string]string{
		"sudo": stubSudo, "goose": stubGoose, "rsync": stubRsync, "systemctl": stubSystemctl, "git": stubGit,
	} {
		write(filepath.Join(stub, name), body, 0o755)
	}
	write(filepath.Join(repo, "bin", binName), "#!/bin/sh\necho svc\n", 0o755)
	write(filepath.Join(repo, "migrations", "001_init.sql"), "-- +goose Up\nSELECT 1;\n", 0o600)
	if preStage != nil {
		preStage(stage)
	}

	src, err := os.ReadFile("deploy-prod.sh")
	if err != nil {
		t.Fatalf("read deploy-prod.sh: %v", err)
	}
	// Point the in-script path at a base that does not exist, so it fails
	// closed instead of reaching for the host's /opt.
	optBase := "OPT_BASE=" + filepath.Join(sandbox, "opt")
	script := strings.Replace(string(src), "OPT_BASE=/opt/"+svc, optBase, 1)
	if !strings.Contains(script, optBase) {
		t.Fatal("OPT_BASE assignment not found — deploy-prod.sh shape changed, update this test")
	}
	write(filepath.Join(deployDir, "deploy-prod.sh"), script, 0o755)
	write(filepath.Join(deployDir, "newest-tag.sh"), "#!/usr/bin/env bash\necho v0.0.0\n", 0o755)
	// The post-migration check queries a real database; stand in for it.
	write(filepath.Join(deployDir, "check-migrations-applied.sh"), "#!/usr/bin/env bash\necho \"check-migrations $*\" >> \"$OPLOG\"\n", 0o755)

	oplog := filepath.Join(sandbox, "ops.log")
	stagedList := filepath.Join(sandbox, "staged.list")
	cmd := exec.Command("bash", "deploy-prod.sh")
	cmd.Dir = deployDir
	cmd.Env = append(os.Environ(),
		"PATH="+stub+":"+os.Getenv("PATH"),
		"OPLOG="+oplog, "STAGED_LIST="+stagedList,
		"GITHUB_SHA=0123456789abcdef0123456789abcdef01234567",
		"APHROLLO_INSTALLER="+filepath.Join(sandbox, "installer"),
		"APHROLLO_RELEASE_STAGING="+filepath.Join(sandbox, "staging"),
		"TMPDIR="+sandbox,
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// The installer path requires the installer file to exist and be executable.
	if env["NO_INSTALLER_FILE"] == "" {
		write(filepath.Join(sandbox, "installer"), "#!/bin/sh\n", 0o755)
	}
	// The script runs from the repo root (as in CI), not from deploy/.
	cmd.Dir = repo
	cmd.Args = []string{"bash", filepath.Join(deployDir, "deploy-prod.sh")}
	out, runErr := cmd.CombinedOutput()
	res := deployRun{output: string(out)}
	if runErr != nil {
		ee, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run deploy-prod.sh: %v", runErr)
		}
		res.exitCode = ee.ExitCode()
	}
	if b, err := os.ReadFile(oplog); err == nil {
		res.ops = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	if b, err := os.ReadFile(stagedList); err == nil {
		res.staged = string(b)
	}
	return res
}

func (r deployRun) has(prefix string) bool {
	for _, op := range r.ops {
		if strings.HasPrefix(op, prefix) {
			return true
		}
	}
	return false
}

func TestInstallerPathStagesAndHandsOff(t *testing.T) {
	r := runDeploy(t, nil, nil)
	if r.exitCode != 0 {
		t.Fatalf("exit %d, output:\n%s", r.exitCode, r.output)
	}
	var calls int
	for _, op := range r.ops {
		if op == "sudo -n "+os.Getenv("APHROLLO_INSTALLER") {
			calls++
		}
		if strings.HasSuffix(op, " "+svc) && strings.HasPrefix(op, "sudo -n ") && !strings.Contains(op, " -l ") {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("want exactly one installer call, got %d: %v", calls, r.ops)
	}
	for _, want := range []string{"./SHA", "./MANIFEST.sha256", "./" + binName} {
		if !strings.Contains(r.staged, want+"\n") {
			t.Errorf("staged tree lacks %s:\n%s", want, r.staged)
		}
	}
	if !r.has("manifest-ok") {
		t.Errorf("the manifest did not verify at install time: %v", r.ops)
	}
}

func TestInstallerPathDoesNotTouchServiceOrOpt(t *testing.T) {
	r := runDeploy(t, nil, nil)
	if r.has("systemctl") || r.has("sudo -n /bin/systemctl") || r.has("sudo /bin/systemctl") {
		t.Errorf("the installer path restarted a unit itself: %v", r.ops)
	}
	if strings.Contains(r.output, "/opt/"+svc) {
		t.Errorf("the installer path writes under /opt/%s:\n%s", svc, r.output)
	}
}

func TestInstallerPathClearsStaleStagingFirst(t *testing.T) {
	r := runDeploy(t, nil, func(stage string) {
		if err := os.WriteFile(filepath.Join(stage, "leftover"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(stage, "olddir"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "olddir", ".hidden"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if r.exitCode != 0 {
		t.Fatalf("exit %d:\n%s", r.exitCode, r.output)
	}
	if strings.Contains(r.staged, "leftover") || strings.Contains(r.staged, "olddir") {
		t.Errorf("stale staging content shipped:\n%s", r.staged)
	}
}

func TestInstallerFailureFailsTheDeploy(t *testing.T) {
	r := runDeploy(t, map[string]string{"INSTALLER_RC": "1"}, nil)
	if r.exitCode == 0 {
		t.Fatalf("a failed installer must fail the deploy; output:\n%s", r.output)
	}
}

func TestFallsBackWhenTheInstallerIsNotGranted(t *testing.T) {
	r := runDeploy(t, map[string]string{"INSTALLER_GRANTED": "0"}, nil)
	for _, op := range r.ops {
		if strings.HasPrefix(op, "sudo -n ") && !strings.Contains(op, " -l ") {
			t.Errorf("called sudo for real without a grant: %q", op)
		}
	}
	if !strings.Contains(r.output, "/releases missing") {
		t.Errorf("without the installer the script should take the in-script path (fails here: the sandbox has no releases dir):\n%s", r.output)
	}
}

func TestFallsBackWhenTheInstallerFileIsAbsent(t *testing.T) {
	r := runDeploy(t, map[string]string{"NO_INSTALLER_FILE": "1"}, nil)
	if r.has("sudo -n -l") {
		t.Errorf("probed sudo for a missing installer: %v", r.ops)
	}
	if !strings.Contains(r.output, "/releases missing") {
		t.Errorf("expected the in-script path:\n%s", r.output)
	}
}
