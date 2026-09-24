package install

import (
	"strings"
	"testing"
)

// A Windows install hands the shim generators a backslash path
// (os.Executable → `C:\Users\me\bin\aphrollo.exe`). The shims are `#!/bin/sh`
// scripts, where an unquoted backslash is an escape character: Git Bash reads
// `exec C:\Users\me\bin\aphrollo.exe` as `C:Usersmebinaphrollo.exe` and every
// gated commit dies with "not found". The shim must embed the path
// slash-normalized AND quoted (quoting also survives `C:\Program Files\…`).
func TestBinShim_WindowsPathIsShSafe(t *testing.T) {
	t.Parallel()
	got := binShim(`C:\Users\me\bin\aphrollo.exe`, "precommit", "")
	want := "exec \"C:/Users/me/bin/aphrollo.exe\" gate precommit \"$@\"\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("binShim windows path:\n got: %q\nwant: %q", got, want)
	}
}

// The POSIX path keeps working — quoted now, but the same binary invocation.
func TestBinShim_PosixPathQuoted(t *testing.T) {
	t.Parallel()
	got := binShim("/usr/local/bin/aphrollo", "precommit", "")
	want := "exec \"/usr/local/bin/aphrollo\" gate precommit \"$@\"\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("binShim posix path:\n got: %q\nwant: %q", got, want)
	}
}

// Same defect, per-repo installer's generator.
func TestShim_WindowsPathIsShSafe(t *testing.T) {
	t.Parallel()
	got := shim(`C:\Users\me\bin\aphrollo.exe`, "precommit")
	if !strings.Contains(got, "\"C:/Users/me/bin/aphrollo.exe\"") {
		t.Fatalf("shim must quote + slash-normalize a windows path, got: %q", got)
	}
	if strings.Contains(got, `\`) && strings.Contains(got, "Users") {
		t.Fatalf("raw backslash path leaked into an sh script: %q", got)
	}
}

// The settings.json hook COMMANDS have the same backslash problem as the git
// shims: Claude Code runs hook commands through a shell (observed: bash on a
// Windows box with Git Bash), where `C:\Users\me\bin\aphrollo.exe tdd
// userpromptsubmit` collapses to `C:Usersmebinaphrollo.exe` — every session
// hook died "command not found" live. The written command must be
// slash-normalized and quoted.
func TestPatchSettings_WindowsBinPathIsShellSafe(t *testing.T) {
	t.Parallel()
	out, _, err := PatchSettings([]byte(`{}`), `C:\Users\me\bin\aphrollo.exe`)
	if err != nil {
		t.Fatal(err)
	}
	cmds := commandStrings(t, out, "UserPromptSubmit")
	if len(cmds) != 1 {
		t.Fatalf("want exactly one UserPromptSubmit command, got %v", cmds)
	}
	want := `"C:/Users/me/bin/aphrollo.exe" gate userpromptsubmit`
	if cmds[0] != want {
		t.Fatalf("hook command:\n got: %q\nwant: %q", cmds[0], want)
	}
}

// And the patch must still recognise its own quoted-path entries as managed on
// a re-run (idempotence), not append duplicates beside them.
func TestPatchSettings_IdempotentWithWindowsQuotedPath(t *testing.T) {
	t.Parallel()
	bin := `C:\Users\me\bin\aphrollo.exe`
	first, _, err := PatchSettings([]byte(`{}`), bin)
	if err != nil {
		t.Fatal(err)
	}
	second, changed, err := PatchSettings(first, bin)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second patch over own output must be a no-op")
	}
	if len(commandStrings(t, second, "UserPromptSubmit")) != 1 {
		t.Fatal("re-patch appended a duplicate hook entry")
	}
}

// The legacy Node-plugin cleanup must catch WINDOWS install paths too: the
// deployed command is `"C:/Program Files/nodejs/node"
// "C:\Users\me\.claude\hooks\tdd-post-edit.js"` — backslashes — while the
// marker list only knew `/hooks/tdd-`. Result observed live: init APPENDED the
// aphrollo hooks beside the node ones and every event double-fired.
func TestPatchSettings_RemovesWindowsPathNodeEntries(t *testing.T) {
	t.Parallel()
	existing := []byte(`{
	  "hooks": {
	    "PostToolUse": [
	      {
	        "hooks": [
	          {
	            "command": "\"C:/Program Files/nodejs/node\" \"C:\\Users\\me\\.claude\\hooks\\tdd-post-edit.js\"",
	            "type": "command"
	          }
	        ],
	        "matcher": "Edit|Write|MultiEdit"
	      }
	    ]
	  }
	}`)
	out, changed, err := PatchSettings(existing, "/usr/local/bin/aphrollo")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("patch must report a change")
	}
	for _, cmd := range commandStrings(t, out, "PostToolUse") {
		if strings.Contains(cmd, "tdd-post-edit.js") {
			t.Fatalf("legacy windows-path node hook survived the patch: %q", cmd)
		}
	}
}
