package dev

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitFor(t *testing.T) {
	for svc, want := range map[string]string{
		"api":   "aphrollo-dev-api.service",
		"rlndx": "aphrollo-dev-rlndx.service",
		"infra": "aphrollo-dev-infra.service",
	} {
		got, err := unitFor(svc)
		if err != nil || got != want {
			t.Errorf("unitFor(%q) = %q,%v; want %q", svc, got, err, want)
		}
	}
	for _, bad := range []string{"", "postgres", "rlndx.service", "../x"} {
		if _, err := unitFor(bad); err == nil {
			t.Errorf("unitFor(%q) should error", bad)
		}
	}
}

// withFakes points systemctl/journalctl at a recorder script and disables sudo,
// returning the path the recorder appends each invocation's argv to.
func withFakes(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	rec := filepath.Join(bin, "rec.log")
	for _, name := range []string{"systemctl", "journalctl"} {
		p := filepath.Join(bin, name)
		body := "#!/bin/sh\necho \"" + name + " $@\" >> " + rec + "\n"
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("APHROLLO_SYSTEMCTL", filepath.Join(bin, "systemctl"))
	t.Setenv("APHROLLO_JOURNALCTL", filepath.Join(bin, "journalctl"))
	t.Setenv("APHROLLO_DEV_SUDO", "0")
	return rec
}

func TestArgv_NoWildcardsAndExactUnits(t *testing.T) {
	t.Setenv("APHROLLO_SYSTEMCTL", "/usr/bin/systemctl")
	t.Setenv("APHROLLO_DEV_SUDO", "0")

	if got := strings.Join(startArgv(), " "); got != "/usr/bin/systemctl start aphrollo-dev.target" {
		t.Errorf("startArgv = %q", got)
	}
	if got := strings.Join(stopArgv(false), " "); got != "/usr/bin/systemctl stop aphrollo-dev-api.service aphrollo-dev-rlndx.service" {
		t.Errorf("stopArgv(false) = %q", got)
	}
	if got := strings.Join(stopArgv(true), " "); got != "/usr/bin/systemctl stop aphrollo-dev.target" {
		t.Errorf("stopArgv(true) = %q", got)
	}
	r, err := restartArgv("rlndx")
	if err != nil || strings.Join(r, " ") != "/usr/bin/systemctl restart aphrollo-dev-rlndx.service" {
		t.Errorf("restartArgv(rlndx) = %q,%v", r, err)
	}
	if _, err := restartArgv("postgres"); err == nil {
		t.Error("restartArgv should reject a non-whitelisted service")
	}
	// status carries no sudo prefix (read-only).
	if statusArgv()[0] == "sudo" {
		t.Error("status should not be sudo-prefixed")
	}
}

func TestSudoPrefixedByDefault(t *testing.T) {
	t.Setenv("APHROLLO_SYSTEMCTL", "/usr/bin/systemctl")
	os.Unsetenv("APHROLLO_DEV_SUDO")
	if os.Geteuid() != 0 && startArgv()[0] != "sudo" {
		t.Errorf("non-root start should be sudo-prefixed: %v", startArgv())
	}
}

func TestLogsArgv(t *testing.T) {
	t.Setenv("APHROLLO_JOURNALCTL", "/usr/bin/journalctl")
	all, err := logsArgv("", 50)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(all, " ")
	for _, u := range allUnits {
		if !strings.Contains(got, "-u "+u) {
			t.Errorf("logsArgv(all) missing %s:\n%s", u, got)
		}
	}
	if !strings.HasSuffix(got, "-n 50") {
		t.Errorf("logsArgv should honor -n: %s", got)
	}
	one, _ := logsArgv("rlndx", 10)
	if !strings.Contains(strings.Join(one, " "), "-u aphrollo-dev-rlndx.service") {
		t.Errorf("logsArgv(rlndx) wrong: %v", one)
	}
	if _, err := logsArgv("postgres", 10); err == nil {
		t.Error("logsArgv should reject a bad service")
	}
}

func TestRestart_ClearsRlndxCache_E2E(t *testing.T) {
	rec := withFakes(t)
	spaces := t.TempDir()
	t.Setenv("APHROLLO_SPACES", spaces)
	// Seed the stale caches that a restart must clear.
	cacheBase := filepath.Join(spaces, ".devclaim", "web", "apps", "rlndx")
	svelte := filepath.Join(cacheBase, ".svelte-kit")
	vite := filepath.Join(cacheBase, "node_modules", ".vite")
	for _, d := range []string{svelte, vite} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var out, errb bytes.Buffer
	if err := Restart("rlndx", &out, &errb); err != nil {
		t.Fatalf("Restart: %v\n%s", err, errb.String())
	}
	if dirExists(svelte) || dirExists(vite) {
		t.Errorf("rlndx restart should have cleared the vite caches")
	}
	got, _ := os.ReadFile(rec)
	if strings.TrimSpace(string(got)) != "systemctl restart aphrollo-dev-rlndx.service" {
		t.Errorf("restart invoked %q", strings.TrimSpace(string(got)))
	}
}

// A non-rlndx restart must NOT touch the rlndx cache dirs.
func TestRestart_ApiLeavesCache_E2E(t *testing.T) {
	rec := withFakes(t)
	spaces := t.TempDir()
	t.Setenv("APHROLLO_SPACES", spaces)
	keep := filepath.Join(spaces, ".devclaim", "web", "apps", "rlndx", ".svelte-kit")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := Restart("api", &out, &errb); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !dirExists(keep) {
		t.Error("api restart must not clear the rlndx cache")
	}
	got, _ := os.ReadFile(rec)
	if strings.TrimSpace(string(got)) != "systemctl restart aphrollo-dev-api.service" {
		t.Errorf("restart invoked %q", strings.TrimSpace(string(got)))
	}
}

func TestUpDownLogs_Invoke_E2E(t *testing.T) {
	rec := withFakes(t)
	var out, errb bytes.Buffer
	if err := Up(&out, &errb); err != nil {
		t.Fatal(err)
	}
	if err := Down(true, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if err := Logs("", 5, &out, &errb); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(rec)
	s := string(got)
	for _, want := range []string{
		"systemctl start aphrollo-dev.target",
		"systemctl stop aphrollo-dev.target",
		"journalctl -u aphrollo-dev-infra.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("recorder missing %q:\n%s", want, s)
		}
	}
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
