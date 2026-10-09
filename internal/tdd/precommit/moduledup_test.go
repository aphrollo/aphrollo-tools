package precommit

import (
	"path/filepath"
	"strings"
	"testing"
)

// vitestDuplicateReact is what vitest printed on fanpyp v1.43.0: every
// component test died of React's own hint, with the stack naming two lanes'
// node_modules beside the merge checkout. The paths are relative to the npm
// root, as vitest prints them.
const vitestDuplicateReact = ` FAIL  src/chat/Message.test.tsx > Message > renders
Error: Invalid hook call. Hooks can only be called inside of the body of a function component. This could happen for one of the following reasons:
2. You might have more than one copy of React in the same app
 ❯ resolveDispatcher ../../feat-chats-l6-shell-prefs/frontend/node_modules/react/cjs/react.development.js:1234:13
 ❯ useState ../../feat-chats-l6-shell-prefs/frontend/node_modules/react/cjs/react.development.js:1567:20
 ❯ render ../../feat-chats-l4-message-state/frontend/node_modules/react-dom/cjs/react-dom.development.js:88:3
 ❯ create ../../feat-chats-l4-message-state/frontend/node_modules/zustand/esm/index.mjs:12:1
 Test Files  41 failed (41)
      Tests  252 failed | 75 passed (327)
`

// installRootNpm stages one npm root so the merge gate runs its suite.
func installRootNpm(t *testing.T) (root string, group rootGroup) {
	t.Helper()
	root = t.TempDir()
	gitInit(t, root)
	write(t, root, "frontend/package.json", `{"name":"fe","scripts":{"test":"vitest run"}}`)
	write(t, root, "frontend/package-lock.json", `{"lockfileVersion":3}`)
	write(t, root, "frontend/src/a.ts", "export const a = 1\n")
	gitDo(t, root, "add", ".")
	groups := stagedRootGroups(root)
	if len(groups) == 0 {
		t.Fatal("no staged root group")
	}
	return root, groups[0]
}

// A run that died of two copies of a module measured the install, not the code.
// The merge is still refused, but as NOT TESTED naming the paths that resolved
// outside the checkout, never as failing tests.
func TestGateRoot_MergeRefusesADuplicateModuleRunAsNotTestedNamingTheOutsidePaths(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, group := installRootNpm(t)
	run := func(r Runner, _ string) SuiteResult {
		return SuiteResult{Passed: false, Output: vitestDuplicateReact, Err: "exit status 1"}
	}

	var res GateResult
	stderr := captureStderr(t, func() { res = gateRoot("premerge", root, group, run, false) })

	if !res.Blocked {
		t.Fatalf("a run that tested nothing let the merge through: %q", res.Message)
	}
	for _, want := range []string{"NOT TESTED", "more than one copy of React", "feat-chats-l6-shell-prefs", "feat-chats-l4-message-state"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal %q lacks %q", res.Message, want)
		}
	}
	if strings.Contains(res.Message, "tests failing") || strings.Contains(stderr, "→ blocked") {
		t.Errorf("a module duplication was reported as failing tests:\n%s\n%s", res.Message, stderr)
	}
	if n := strings.Count(stderr, "NOT TESTED"); n != 1 {
		t.Errorf("the refusal was printed %d times, want once:\n%s", n, stderr)
	}
	if logged := gateLogText(t, cfg); !strings.Contains(logged, "infra-failed") {
		t.Errorf("gate.log wants infra-failed:\n%s", logged)
	}
}

// A path inside the checkout is not named as outside it.
func TestModuleDuplication_NamesOnlyTheNodeModulesOutsideTheCheckout(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "node_modules", "react")
	output := "Invalid hook call\n ❯ a " + filepath.ToSlash(inside) + "/index.js:1:1\n ❯ b ../../elsewhere/frontend/node_modules/react-dom/index.js:2:2\n"

	sig, outside := moduleDuplication(root, root, SuiteResult{Output: output})

	if sig != "Invalid hook call" {
		t.Errorf("signature = %q, want Invalid hook call", sig)
	}
	if len(outside) != 1 || !strings.HasSuffix(filepath.ToSlash(outside[0]), "elsewhere/frontend/node_modules") {
		t.Errorf("outside = %v, want only elsewhere/frontend/node_modules", outside)
	}
}

// Ordinary failing tests carry no signature and stay failing tests.
func TestModuleDuplication_AnOrdinaryFailureHasNoSignature(t *testing.T) {
	if sig, _ := moduleDuplication(t.TempDir(), t.TempDir(), SuiteResult{Output: "FAIL src/a.test.ts > adds\nAssertionError: expected 2 to be 3\n"}); sig != "" {
		t.Errorf("signature = %q, want none", sig)
	}
}
