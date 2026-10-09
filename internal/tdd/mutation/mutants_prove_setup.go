package mutation

import "strings"

// proveSetupSignatures are the phrases a test prints when its own fixture
// could not be built in the proof's copy: a path past the platform's limit.
// Data: a new one is a row here. Matched case-insensitively.
var proveSetupSignatures = []string{
	"Filename too long",
	"The filename or extension is too long",
	"path too long",
}

// proveSetupFailure is the signature a failed run's output carries when the
// failure is the copy's setup and not the mutated line, "" when it carries none.
func proveSetupFailure(output string) string {
	low := strings.ToLower(output)
	for _, s := range proveSetupSignatures {
		if strings.Contains(low, strings.ToLower(s)) {
			return s
		}
	}
	return ""
}
