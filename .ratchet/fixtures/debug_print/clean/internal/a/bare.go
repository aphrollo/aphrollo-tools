package a

import "log"

func loadThing() error {
	// debug-ok: escape hatch under test, not left-over debug output
	log.Print("loadThing: entered")
	return nil
}
