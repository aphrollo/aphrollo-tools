package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// hangingBinary writes an executable that ignores its arguments and holds for
// 30 seconds: a candidate whose self-check never answers.
func hangingBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "hang.bat")
		if err := os.WriteFile(path, []byte("@echo off\r\nping -n 30 127.0.0.1 >nul\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, "hang")
	if err := proc.WriteExecutable(path, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// `aphrollo update` runs the candidate's self-check and the post-swap init
// with no clock of their own; one that hangs holds the update, and every
// process it started, for as long as it likes (#997).
func TestRunSmokeCheck_KillsACandidateThatNeverAnswers(t *testing.T) {
	start := time.Now()
	err := runSmokeCheck(hangingBinary(t), 300*time.Millisecond)
	if err == nil {
		t.Fatal("a self-check that never answered was reported as passed")
	}
	if !strings.Contains(err.Error(), "no answer within 300ms") {
		t.Fatalf("error does not say it timed out: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the hung self-check held the update for %s, want it killed near its 300ms budget", elapsed)
	}
}

func TestRunSmokeCheck_PassesACandidateThatAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a sh script; the timeout half above runs everywhere")
	}
	ok := filepath.Join(t.TempDir(), "ok")
	if err := proc.WriteExecutable(ok, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runSmokeCheck(ok, 5*time.Second); err != nil {
		t.Fatalf("a candidate that exits 0 failed its self-check: %v", err)
	}
}

func TestRunSmokeCheck_ReportsAFailingCandidateWithoutCallingItATimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a sh script")
	}
	bad := filepath.Join(t.TempDir(), "bad")
	if err := proc.WriteExecutable(bad, []byte("#!/bin/sh\necho broke\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runSmokeCheck(bad, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "broke") || strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("error = %v, want the candidate's own output and no timeout wording", err)
	}
}

func TestRunInstalledInit_KillsAnInitThatNeverReturns(t *testing.T) {
	start := time.Now()
	code, err := runInstalledInit(hangingBinary(t), nil, &strings.Builder{}, &strings.Builder{}, 300*time.Millisecond)
	if err == nil || code == 0 {
		t.Fatalf("a hung init reported code=%d err=%v, want a failure", code, err)
	}
	if !strings.Contains(err.Error(), "no answer within 300ms") {
		t.Fatalf("error does not say it timed out: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the hung init held the update for %s", elapsed)
	}
}

// ratchet: test_removed TestBoundedCommand_LeavesTheCommandsOwnErrorAloneBeforeTheDeadline: boundedCommand is gone, replaced by boundedRun over internal/run, whose pipe grace and error handling TestBoundedRun_AFailingChildIsNotCalledATimeout and run's own tests prove.

// A budget that parsed to zero would kill every subprocess the update starts at
// once; one that grew without bound is no budget. Each sits between "seconds"
// and "the afternoon", and a slow build outlasts a self-check.
func TestUpdateBudgets_AreBoundedAndOrdered(t *testing.T) {
	if smokeCheckBudget < 10*time.Second || smokeCheckBudget >= initBudget || initBudget >= buildBudget || buildBudget > time.Hour {
		t.Fatalf("smoke=%s init=%s build=%s: want 10s <= smoke < init < build <= 1h", smokeCheckBudget, initBudget, buildBudget)
	}
}
