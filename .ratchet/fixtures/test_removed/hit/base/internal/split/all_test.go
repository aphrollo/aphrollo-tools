package split

import "testing"

func TestMovedOut(t *testing.T) {
	if 4+4 != 8 {
		t.Fatal("arithmetic broke")
	}
}

func TestLostInTheSplit(t *testing.T) {
	if 5+5 != 10 {
		t.Fatal("arithmetic broke")
	}
}
