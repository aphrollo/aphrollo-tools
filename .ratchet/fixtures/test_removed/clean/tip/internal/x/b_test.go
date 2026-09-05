package x

import "testing"

func TestFoo(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("arithmetic broke")
	}
}
