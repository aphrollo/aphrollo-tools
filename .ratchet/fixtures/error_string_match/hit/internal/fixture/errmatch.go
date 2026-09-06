package fixture

import "strings"

// classifyByText is the shape this law forbids: matching an error by its
// rendered message instead of a sentinel or a typed error.
func classifyByText(err error) bool {
	if strings.Contains(err.Error(), "not found") {
		return true
	}
	return err.Error() == "EOF"
}

// classifyRawText hits the second trigger shape: the error's own rendered
// text held directly under a variable named err, searched without ever
// calling .Error() on it.
func classifyRawText(output []byte) bool {
	err := string(output)
	return strings.Contains(err, "timeout")
}
