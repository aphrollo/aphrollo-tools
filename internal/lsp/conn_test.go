package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

// pipeConn wires a Conn to a fake server goroutine over two pipes and returns
// the server's read/write ends.
func pipeConn(t *testing.T) (c *Conn, serverIn *bufio.Reader, serverOut io.Writer) {
	t.Helper()
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	c = NewConn(cw, cr)
	t.Cleanup(func() { c.Close() })
	return c, bufio.NewReader(sr), sw
}

func TestConn_Call_RoundTrip(t *testing.T) {
	c, in, out := pipeConn(t)

	go func() {
		body, err := readFrame(in)
		if err != nil {
			return
		}
		var req message
		_ = json.Unmarshal(body, &req)
		if req.Method != "ping" {
			t.Errorf("server got method %q, want ping", req.Method)
		}
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`, string(req.ID))
		_ = writeFrame(out, []byte(resp))
	}()

	var res struct {
		OK bool `json:"ok"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Call(ctx, "ping", nil, &res); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false, want true")
	}
}

// A server-initiated request (e.g. workspace/configuration during init) must be
// answered, or the language server blocks. With no handler we reply null.
func TestConn_RepliesToServerRequest(t *testing.T) {
	c, in, out := pipeConn(t)

	go func() {
		body, err := readFrame(in)
		if err != nil {
			return
		}
		var call message
		_ = json.Unmarshal(body, &call)

		// Server asks us something mid-call.
		_ = writeFrame(out, []byte(`{"jsonrpc":"2.0","id":99,"method":"workspace/configuration","params":{}}`))
		reply, err := readFrame(in)
		if err != nil {
			t.Errorf("reading server-request reply: %v", err)
			return
		}
		var rep message
		_ = json.Unmarshal(reply, &rep)
		if string(rep.ID) != "99" {
			t.Errorf("server-request reply id = %s, want 99", rep.ID)
		}

		// Now answer the original call.
		_ = writeFrame(out, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`, string(call.ID))))
	}()

	var res struct {
		OK bool `json:"ok"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Call(ctx, "ping", nil, &res); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false, want true")
	}
}

// When a Call's context is cancelled before the server replies, its pending
// correlation entry must be removed — otherwise the map retains an orphaned
// channel for a response that, if it ever arrives, has no waiter.
func TestConn_Call_CancelCleansPending(t *testing.T) {
	c, in, _ := pipeConn(t)
	// Drain the request so the synchronous pipe write completes, but never send a
	// response — the Call then blocks waiting for a reply until its ctx fires.
	go func() {
		for {
			if _, err := readFrame(in); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "ping", nil, nil); err == nil {
		t.Fatalf("Call: want context error, got nil")
	}

	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending entries after cancelled Call = %d, want 0", n)
	}
}

// Wait must return once the read loop ends, so teardown can join the goroutine
// instead of leaking it. Closing the server->client write end gives the reader
// EOF, which is exactly what killing the server process does to its stdout.
func TestConn_WaitJoinsReadLoop(t *testing.T) {
	cr, sw := io.Pipe() // server -> client
	_, cw := io.Pipe()  // client -> server (unused)
	c := NewConn(cw, cr)

	_ = sw.Close() // EOF to the read loop

	done := make(chan struct{})
	go func() { c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after the read loop exited")
	}
}

// An RPC error from the server must surface as a Go error, not a silent
// zero-value result.
func TestConn_Call_ServerError(t *testing.T) {
	c, in, out := pipeConn(t)

	go func() {
		body, err := readFrame(in)
		if err != nil {
			return
		}
		var req message
		_ = json.Unmarshal(body, &req)
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"boom"}}`, string(req.ID))
		_ = writeFrame(out, []byte(resp))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Call(ctx, "ping", nil, nil)
	if err == nil {
		t.Fatalf("Call: want error, got nil")
	}
}
