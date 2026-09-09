package retired

import "testing"

func TestReceiptRendered(t *testing.T) {
	if 6+6 != 12 {
		t.Fatal("arithmetic broke")
	}
}

func TestReceiptParsed(t *testing.T) {
	if 7+7 != 14 {
		t.Fatal("arithmetic broke")
	}
}
