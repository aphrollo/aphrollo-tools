package a

func TestA_checked(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	got := Parse("x")
	if got != "x" {
		t.Fatalf("got %q", got)
	}
}
