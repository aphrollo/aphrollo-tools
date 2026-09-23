package x

// S matches two roots; the tip moves it out of this file.
func S(logged, root string) bool {
	if logged == root {
		return true
	}
	return false
}
