package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// message is a JSON-RPC 2.0 envelope covering requests, responses and
// notifications. Fields are omitted when empty so a single type serialises all
// three shapes.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// ServerRequestHandler answers a server->client request. Returning a nil result
// with nil error replies with JSON null. Unhandled requests default to null so
// the server never blocks waiting on us.
type ServerRequestHandler func(method string, params json.RawMessage) (any, error)

// Conn is a JSON-RPC 2.0 connection over an LSP stdio transport. A background
// read loop correlates responses to outstanding Calls and dispatches
// server-initiated requests and notifications.
type Conn struct {
	w   io.Writer
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]chan *message

	// OnServerRequest handles server->client requests; if nil, they are
	// answered with null. OnNotification observes server notifications.
	//
	// The read loop dispatches each server request in its OWN goroutine
	// (serveRequest) so a slow handler can't stall delivery of the response the
	// caller is blocked on. Those goroutines are not joined by Wait, so the
	// handler MUST return promptly: one that blocks indefinitely leaks its
	// goroutine for the process's lifetime. The handlers we register
	// (workspace/configuration, client/registerCapability) just return canned
	// data, so they never block.
	OnServerRequest ServerRequestHandler
	OnNotification  func(method string, params json.RawMessage)

	closeOnce sync.Once
	closed    chan struct{}
	readErr   error

	loopDone chan struct{} // closed when readLoop returns
}

// NewConn starts a Conn reading framed messages from r and writing to w.
func NewConn(w io.Writer, r io.Reader) *Conn {
	c := &Conn{
		w:        w,
		pending:  map[int]chan *message{},
		closed:   make(chan struct{}),
		loopDone: make(chan struct{}),
	}
	go c.readLoop(bufio.NewReader(r))
	return c
}

// Close stops the read loop and fails any in-flight calls.
func (c *Conn) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// Wait blocks until the background read loop has exited. Once the server
// process is killed its stdout closes, readFrame sees EOF, and the loop
// returns; joining it on teardown guarantees no goroutine outlives cleanup.
func (c *Conn) Wait() { <-c.loopDone }

func (c *Conn) readLoop(r *bufio.Reader) {
	defer close(c.loopDone)
	for {
		body, err := readFrame(r)
		if err != nil {
			c.fail(err)
			return
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			c.fail(fmt.Errorf("decode message: %w", err))
			return
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go c.serveRequest(&m)
		case m.Method != "":
			if c.OnNotification != nil {
				c.OnNotification(m.Method, m.Params)
			}
		default:
			c.deliver(&m)
		}
	}
}

func (c *Conn) serveRequest(m *message) {
	var (
		result any
		err    error
	)
	if c.OnServerRequest != nil {
		result, err = c.OnServerRequest(m.Method, m.Params)
	}
	reply := message{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		reply.Error = &rpcError{Code: -32603, Message: err.Error()}
	} else {
		raw, mErr := json.Marshal(result)
		if mErr != nil {
			reply.Error = &rpcError{Code: -32603, Message: mErr.Error()}
		} else {
			reply.Result = raw
		}
	}
	_ = c.send(&reply)
}

func (c *Conn) deliver(m *message) {
	var id int
	if err := json.Unmarshal(m.ID, &id); err != nil {
		return // response to an id we don't track
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.Close()
}

// Call sends a request and unmarshals the result into out (which may be nil).
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan *message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(&message{JSONRPC: "2.0", ID: jsonInt(id), Method: method, Params: rawParams}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		if c.readErr != nil {
			return c.readErr
		}
		return fmt.Errorf("connection closed")
	case m, ok := <-ch:
		if !ok {
			if c.readErr != nil {
				return c.readErr
			}
			return fmt.Errorf("connection closed")
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	}
}

// Notify sends a notification (no response expected).
func (c *Conn) Notify(method string, params any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.send(&message{JSONRPC: "2.0", Method: method, Params: rawParams})
}

func (c *Conn) send(m *message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.w, body)
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}

func jsonInt(i int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf("%d", i))
}
