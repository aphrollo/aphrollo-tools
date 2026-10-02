package rollback

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Every file this package persists carries its own format number, so a binary
// that meets a file written by a newer one reads it as absent and leaves it
// exactly where it is, and a file that does not parse at all is moved aside as
// evidence of a torn write instead of being read or deleted.

type schemaStamp struct {
	Schema int `json:"schema"`
}

// readVersioned decodes the JSON file at path into v. found is false for a file
// that is absent, unparsable or of a newer format than current; newer is true
// for the last of those alone, telling the caller its own writes would clobber
// a file it cannot read.
func readVersioned(path string, v any, current int) (found, newer bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	var stamp schemaStamp
	if json.Unmarshal(data, &stamp) != nil {
		quarantine(path)
		return false, false
	}
	if stamp.Schema > current {
		return false, true
	}
	if json.Unmarshal(data, v) != nil {
		quarantine(path)
		return false, false
	}
	return true, false
}

// quarantine moves an unparsable file aside under a timestamped name. A rename
// that fails leaves the file in place, which the next read reports again.
func quarantine(path string) {
	_ = os.Rename(path, fmt.Sprintf("%s.corrupt-%d", path, time.Now().UTC().Unix()))
}

// newerFile is the refusal a writer returns for a file of a newer format.
func newerFile(path string) error {
	return fmt.Errorf("%s was written by a newer aphrollo; this one will not read or overwrite it", path)
}
