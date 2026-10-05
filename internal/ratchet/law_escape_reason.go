package ratchet

import "fmt"

// parseEscapeReason reads `escape_reason`: true (the default) asks a reason
// after the escape token, false (EscapeBare) admits the token alone. It returns
// whether the law is bare.
func parseEscapeReason(doc *tomlDoc) (bool, error) {
	v, ok := doc.value("", "escape_reason")
	if !ok {
		return false, nil
	}
	if v.kind != tomlBool {
		return false, fmt.Errorf("escape_reason is a boolean, got %s", v.kind)
	}
	return !v.b, nil
}
