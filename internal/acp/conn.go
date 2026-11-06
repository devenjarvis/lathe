// Package acp is a minimal client for the Agent Client Protocol
// (https://agentclientprotocol.com): JSON-RPC 2.0, one message per line, over
// the stdio of an agent process the user chose. Lathe uses it to host the
// user's own coding agent behind the web UI's Ask drawer.
//
// The package speaks only the client half of the protocol Lathe needs —
// initialize, session/new, session/prompt, session/cancel, the session/update
// stream and session/request_permission. It advertises no filesystem or
// terminal capabilities, so the agent uses its own tools (under its own
// permission prompts, which surface here as permission requests). It never
// calls a model API itself: the agent process does all model work.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// ProtocolVersion is the ACP major version this client speaks.
const ProtocolVersion = 1

// ErrClosed is returned by calls made on (or pending when) the connection
// closes — typically because the agent process exited.
var ErrClosed = errors.New("acp: connection closed")

// RPCError is a JSON-RPC error object returned by the agent.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("acp: %s (code %d)", e.Message, e.Code) }

// JSON-RPC error codes this client sends back.
const (
	codeMethodNotFound = -32601
	codeInternal       = -32603
)

// message is the union of every JSON-RPC 2.0 frame. A frame with a method is a
// request (with id) or notification (without); one without is a response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Handler receives what the agent sends unprompted. HandleRequest must return
// a result (marshalled as the response) or an error; it runs on its own
// goroutine, so it may block (a permission prompt waits on the reader).
// HandleNotification runs on the read loop, in order, and must not block.
type Handler interface {
	HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, error)
	HandleNotification(method string, params json.RawMessage)
}

// Conn is one JSON-RPC connection to an agent.
type Conn struct {
	w       io.Writer
	writeMu sync.Mutex
	handler Handler

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan message
	err     error // set once, when the read loop ends

	done     chan struct{}
	ctx      context.Context // cancelled on close, for in-flight handlers
	cancel   context.CancelFunc
	handlers sync.WaitGroup // requests being answered
}

// NewConn starts reading frames from r and returns a connection that writes
// to w. The read loop ends (and Done closes) when r hits EOF or an error.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		w:       w,
		handler: h,
		pending: map[string]chan message{},
		done:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	go c.readLoop(r)
	return c
}

// Done is closed when the connection stops reading.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Wait blocks until the connection stops reading and every request it had
// received has been answered (or its answer failed to send). A server uses it
// to finish in-flight work before exiting when its input closes.
func (c *Conn) Wait() {
	<-c.done
	c.handlers.Wait()
}

// Err reports why the connection closed (nil while it is open).
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	// Agents send whole files and long tool output in one frame.
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			// Not ours to interpret (a stray log line); skip it.
			continue
		}
		c.dispatch(m)
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.close(err)
}

func (c *Conn) close(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = fmt.Errorf("%w: %v", ErrClosed, err)
	pending := c.pending
	c.pending = map[string]chan message{}
	c.mu.Unlock()
	c.cancel()
	for _, ch := range pending {
		close(ch)
	}
	close(c.done)
}

func (c *Conn) dispatch(m message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		c.handlers.Add(1)
		go func() {
			defer c.handlers.Done()
			c.serveRequest(m)
		}()
	case m.Method != "":
		if c.handler != nil {
			c.handler.HandleNotification(m.Method, m.Params)
		}
	case len(m.ID) > 0:
		c.mu.Lock()
		ch, ok := c.pending[string(m.ID)]
		delete(c.pending, string(m.ID))
		c.mu.Unlock()
		if ok {
			ch <- m
		}
	}
}

func (c *Conn) serveRequest(m message) {
	resp := message{JSONRPC: "2.0", ID: m.ID}
	var (
		result any
		err    error
	)
	if c.handler == nil {
		err = &RPCError{Code: codeMethodNotFound, Message: "method not found: " + m.Method}
	} else {
		result, err = c.handler.HandleRequest(c.ctx, m.Method, m.Params)
	}
	if err != nil {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			rpcErr = &RPCError{Code: codeInternal, Message: err.Error()}
		}
		resp.Error = rpcErr
	} else {
		raw, merr := json.Marshal(result)
		if merr != nil {
			resp.Error = &RPCError{Code: codeInternal, Message: merr.Error()}
		} else {
			resp.Result = raw
		}
	}
	_ = c.send(resp)
}

func (c *Conn) send(m message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(raw)
	return err
}

// Call sends a request and decodes its result into out (which may be nil).
// It returns when the agent answers, ctx ends, or the connection closes.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := json.RawMessage(strconv.FormatInt(c.nextID, 10))
	ch := make(chan message, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()

	if err := c.send(message{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}); err != nil {
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
		return fmt.Errorf("acp: send %s: %w", method, err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return c.Err()
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("acp: decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification (no response expected).
func (c *Conn) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(message{JSONRPC: "2.0", Method: method, Params: raw})
}
