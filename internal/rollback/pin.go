// Package rollback holds what lets the box binary move back: the pin override
// `aphrollo update --to` records in the gate state dir, the record kept beside
// each installed binary, and the event every swap, pin and unpin writes.
package rollback

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// PinSchema is the format number the pin file carries.
const PinSchema = 1

const pinFileName = "binary-pin.json"

// Pin is the box's override: stay on Commit, which Ref named when it was set.
// Ref is what the operator typed (a tag), or the short sha when they typed one.
type Pin struct {
	Schema int    `json:"schema"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	At     string `json:"at,omitempty"` // UTC RFC3339, when the pin was set
}

// PinState is what ReadPin found.
type PinState int

const (
	// Unpinned: no pin, so the box follows origin/main.
	Unpinned PinState = iota
	// Pinned: a pin this binary understands.
	Pinned
	// PinUnreadable: a pin file of a newer format, left untouched.
	PinUnreadable
)

// PinPath is the pin file in the gate state dir, "" when there is no state dir.
func PinPath() string {
	dir := core.StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, pinFileName)
}

// ReadPin reports the box's pin. A pin that names no commit is no pin.
func ReadPin() (Pin, PinState) {
	path := PinPath()
	if path == "" {
		return Pin{}, Unpinned
	}
	var p Pin
	found, newer := readVersioned(path, &p, PinSchema)
	switch {
	case newer:
		return Pin{}, PinUnreadable
	case !found || p.Commit == "":
		return Pin{}, Unpinned
	}
	return p, Pinned
}

// WritePin records p as the box's pin, replacing any pin of this format.
func WritePin(p Pin) error {
	path := PinPath()
	if path == "" {
		return errors.New("no gate state dir to record the pin in")
	}
	if _, newer := readVersioned(path, &Pin{}, PinSchema); newer {
		return newerFile(path)
	}
	p.Schema = PinSchema
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return core.WriteFileAtomic(path, data)
}

// ClearPin removes the pin and reports whether there was one to remove.
func ClearPin() (cleared bool, err error) {
	path := PinPath()
	if path == "" {
		return false, nil
	}
	if _, newer := readVersioned(path, &Pin{}, PinSchema); newer {
		return false, newerFile(path)
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ShortSHA is the 7-character form `aphrollo version` prints, unchanged for a
// shorter string.
func ShortSHA(sha string) string {
	return sha[:min(len(sha), 7)]
}

// Describe names what the pin holds: a tag with its commit, a bare sha once.
func (p Pin) Describe() string {
	if strings.HasPrefix(p.Commit, p.Ref) {
		return ShortSHA(p.Commit)
	}
	return fmt.Sprintf("%s (%s)", p.Ref, ShortSHA(p.Commit))
}

// PinNotice is the session-start line for a pinned box: what it is pinned to
// and how to leave the pin. ok is false when there is nothing to say.
func PinNotice() (line string, ok bool) {
	p, state := ReadPin()
	switch state {
	case Pinned:
		return fmt.Sprintf("aphrollo binary is pinned to %s; run aphrollo update --unpin to follow origin/main again", p.Describe()), true
	case PinUnreadable:
		return "aphrollo binary pin was written by a newer aphrollo and is left alone; change it with that aphrollo", true
	}
	return "", false
}
