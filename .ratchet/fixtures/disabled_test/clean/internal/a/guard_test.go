package a

func TestA_guard(t *testing.T) {
	t.Skip("no display") // skip-ok: needs a display
	// skip-ok: needs root
	t.Skip("not root")
	s := "t.Skip(1)"
	reader.Skip(4)
}
