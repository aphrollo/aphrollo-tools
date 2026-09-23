package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Two boxes, two binaries, one shared state dir — or one box mid-upgrade.
// Every JSON file the gate writes therefore carries the schema it was written
// at, and every reader asks before it interprets. The two answers a reader can
// give without the stamp are both wrong: guessing at unknown fields silently
// drops what the other binary recorded, and resetting the file destroys it.
//
// So a file whose schema EXCEEDS this binary's reads as ABSENT and is left
// exactly where it is, and a file whose JSON does not parse at all is renamed
// aside rather than discarded — a torn write is evidence, and a state file
// that vanished silently is the one failure nobody can debug afterwards.

// StateSchema is the version stamped into every state file this binary writes.
const StateSchema = 1

// schemaStamp is the one field every state file shares.
type schemaStamp struct {
	Schema int `json:"schema"`
}

// stateNoticed dedupes the per-file notices to ONE per process: a hook that
// reads the same unreadable file three times must not write three log lines.
var stateNoticed sync.Map

// noteStateOnce logs `<verdict>:<file>` to gate.log the first time this
// process meets it.
func noteStateOnce(verdict, path string) {
	key := verdict + "\x00" + path
	if _, seen := stateNoticed.LoadOrStore(key, true); seen {
		return
	}
	AppendGateLog("state", filepath.Dir(path), "state file", verdict+":"+filepath.Base(path), 0)
}

// readStateJSON decodes a schema-stamped state file into v. It reports false
// — read nothing, the caller proceeds as if the file did not exist — when the
// file is absent, was written at a NEWER schema, or does not parse. usable is
// false for the newer case only, telling the caller its own writes would
// clobber a file it cannot read.
func readStateJSON(path string, v any) (ok, usable bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, true
	}
	var stamp schemaStamp
	if err := json.Unmarshal(data, &stamp); err != nil {
		quarantineState(path)
		return false, true
	}
	if stamp.Schema > StateSchema {
		noteStateOnce("state-newer", path)
		return false, false
	}
	if err := json.Unmarshal(data, v); err != nil {
		quarantineState(path)
		return false, true
	}
	return true, true
}

// quarantineState moves an unparseable state file aside under a timestamped
// name and logs it once. Best-effort: a rename that fails leaves the file in
// place, which the next read reports again rather than acting on.
func quarantineState(path string) {
	noteStateOnce("state-corrupt", path)
	_ = os.Rename(path, fmt.Sprintf("%s.corrupt-%d", path, time.Now().UTC().Unix()))
}

// gateLogMetaPath is gate.log's schema stamp. The log itself is append-only
// text written by several processes at once, so the version cannot ride in
// its lines; it rides beside it.
func gateLogMetaPath() string {
	path := GateLogPath()
	if path == "" {
		return ""
	}
	return path + ".meta"
}

// stampGateLogSchema writes the sibling stamp when there is none. Called on
// every append and cheap: one Stat on a file that exists after the first log
// line of the dir's life.
func stampGateLogSchema() {
	path := gateLogMetaPath()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return
	}
	data, err := json.Marshal(schemaStamp{Schema: StateSchema})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// GateLogNewerSchema reports the schema gate.log was written at when it
// EXCEEDS this binary's, so a reader (`gate stats`) says so instead of
// tallying lines whose shape it cannot vouch for. An absent or unreadable
// stamp is a log from before the stamp existed, which this binary reads fine.
func GateLogNewerSchema() (int, bool) {
	path := gateLogMetaPath()
	if path == "" {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var stamp schemaStamp
	if err := json.Unmarshal(data, &stamp); err != nil {
		return 0, false
	}
	if stamp.Schema <= StateSchema {
		return 0, false
	}
	noteStateOnce("state-newer", path)
	return stamp.Schema, true
}
