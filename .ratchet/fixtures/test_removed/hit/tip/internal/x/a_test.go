package x

import "testing"

func TestBar(t *testing.T) {
	if 2+2 != 4 {
		t.Fatal("arithmetic broke")
	}
}
