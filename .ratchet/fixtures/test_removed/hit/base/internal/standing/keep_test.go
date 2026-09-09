package standing

import "testing"

func TestKept(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("arithmetic broke")
	}
}

func TestQuietlyDropped(t *testing.T) {
	if 3+3 != 6 {
		t.Fatal("arithmetic broke")
	}
}
