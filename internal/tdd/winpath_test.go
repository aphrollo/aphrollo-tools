package tdd

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
	got := binShim(`C:\Users\me\bin\aphrollo.exe`, "precommit")
	want := "#!/bin/sh\n" + installMarker + "\nexec \"C:/Users/me/bin/aphrollo.exe\" tdd precommit \"$@\"\n"
	if got != want {
		t.Fatalf("binShim windows path:\n got: %q\nwant: %q", got, want)
	}
}

// The POSIX path keeps working — quoted now, but the same binary invocation.
func TestBinShim_PosixPathQuoted(t *testing.T) {
	got := binShim("/usr/local/bin/aphrollo", "precommit")
	want := "#!/bin/sh\n" + installMarker + "\nexec \"/usr/local/bin/aphrollo\" tdd precommit \"$@\"\n"
	if got != want {
		t.Fatalf("binShim posix path:\n got: %q\nwant: %q", got, want)
	}
}

// Same defect, per-repo installer's generator.
func TestShim_WindowsPathIsShSafe(t *testing.T) {
	got := shim(`C:\Users\me\bin\aphrollo.exe`, "precommit")
	if !strings.Contains(got, "\"C:/Users/me/bin/aphrollo.exe\"") {
		t.Fatalf("shim must quote + slash-normalize a windows path, got: %q", got)
	}
	if strings.Contains(got, `\`) && strings.Contains(got, "Users") {
		t.Fatalf("raw backslash path leaked into an sh script: %q", got)
	}
}

// The legacy Node-plugin cleanup must catch WINDOWS install paths too: the
// deployed command is `"C:/Program Files/nodejs/node"
// "C:\Users\me\.claude\hooks\tdd-post-edit.js"` — backslashes — while the
// marker list only knew `/hooks/tdd-`. Result observed live: init APPENDED the
// aphrollo hooks beside the node ones and every event double-fired.
func TestPatchSettings_RemovesWindowsPathNodeEntries(t *testing.T) {
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
