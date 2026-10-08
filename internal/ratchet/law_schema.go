package ratchet

import "fmt"

// parseSchema reads the optional `schema = N` version stamp. Absent is
// SchemaVersion — every law written before the key existed. A value ABOVE
// SchemaVersion means the file came from a newer binary: newer=true switches
// the rest of parsing to lenient, so an unknown key is skipped instead of
// rejected. A non-integer or non-positive value is a broken law, not a
// future one, and is rejected either way.
func parseSchema(doc *tomlDoc) (schema int, newer bool, err error) {
	v, ok := doc.value("", "schema")
	if !ok {
		return SchemaVersion, false, nil
	}
	if v.kind != tomlInt || v.i < 1 {
		return 0, false, fmt.Errorf("schema is a positive integer version, got %s", v.kind)
	}
	return v.i, v.i > SchemaVersion, nil
}

func requiredString(doc *tomlDoc, section, key string) (string, error) {
	v, ok := doc.value(section, key)
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	if v.kind != tomlString || v.s == "" {
		return "", fmt.Errorf("%s is a non-empty string", key)
	}
	return v.s, nil
}
