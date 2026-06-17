package lsp

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestFrame_RoundTrip(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`)

	var buf bytes.Buffer
	if err := writeFrame(&buf, body); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}

	wantHeader := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if got := buf.String(); got[:len(wantHeader)] != wantHeader {
		t.Fatalf("header = %q, want prefix %q", got, wantHeader)
	}

	got, err := readFrame(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("readFrame = %q, want %q", got, body)
	}
}

// A present-but-negative Content-Length is a distinct protocol violation from a
// missing header; the error must say so (naming the bad value) rather than the
// misleading "missing Content-Length header".
func TestFrame_NegativeContentLength(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("Content-Length: -5\r\n\r\n"))
	_, err := readFrame(r)
	if err == nil {
		t.Fatalf("readFrame: want error for negative Content-Length, got nil")
	}
	if msg := err.Error(); !strings.Contains(msg, "-5") || strings.Contains(msg, "missing") {
		t.Fatalf("error = %q, want it to name the negative value -5 and not claim 'missing'", msg)
	}
}

// The reader must consume exactly one frame so a second frame on the same
// stream reads back independently.
func TestFrame_SequentialFrames(t *testing.T) {
	var buf bytes.Buffer
	first := []byte(`{"id":1}`)
	second := []byte(`{"id":2}`)
	_ = writeFrame(&buf, first)
	_ = writeFrame(&buf, second)

	r := bufio.NewReader(&buf)
	g1, err := readFrame(r)
	if err != nil {
		t.Fatalf("readFrame 1: %v", err)
	}
	g2, err := readFrame(r)
	if err != nil {
		t.Fatalf("readFrame 2: %v", err)
	}
	if !bytes.Equal(g1, first) || !bytes.Equal(g2, second) {
		t.Fatalf("got %q,%q want %q,%q", g1, g2, first, second)
	}
}
