// Package discord implements the minimal subset of Discord's local RPC
// (IPC over \\.\pipe\discord-ipc-N) needed to set Rich Presence.
package discord

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
	opPing      = 3
	opPong      = 4
)

// Activity types allowed by SET_ACTIVITY.
const (
	TypePlaying   = 0
	TypeListening = 2
	TypeWatching  = 3
	TypeCompeting = 5
)

// Values for Activity.StatusDisplayType.
const (
	DisplayName    = 0
	DisplayState   = 1
	DisplayDetails = 2
)

type Assets struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
	SmallImage string `json:"small_image,omitempty"`
	SmallText  string `json:"small_text,omitempty"`
}

type Timestamps struct {
	Start int64 `json:"start,omitempty"` // unix milliseconds
	End   int64 `json:"end,omitempty"`
}

type Activity struct {
	Type              int         `json:"type"`
	Details           string      `json:"details,omitempty"`
	State             string      `json:"state,omitempty"`
	Assets            *Assets     `json:"assets,omitempty"`
	Timestamps        *Timestamps `json:"timestamps,omitempty"`
	StatusDisplayType *int        `json:"status_display_type,omitempty"`
}

// User is the subset of the READY payload we surface in the UI.
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

type message struct {
	Cmd   string          `json:"cmd"`
	Evt   string          `json:"evt"`
	Nonce string          `json:"nonce"`
	Data  json.RawMessage `json:"data"`
}

var ErrClosed = errors.New("discord connection closed")

// Client is a single IPC connection. It is safe for concurrent use.
// When the pipe breaks the client becomes unusable; create a new one.
type Client struct {
	f       *os.File
	wmu     sync.Mutex
	nonce   atomic.Uint64
	pmu     sync.Mutex
	pending map[string]chan message
	done    chan struct{}
	errOnce sync.Once
	err     error
	User    User
}

// Dial tries discord-ipc-0..9 and performs the handshake with clientID.
func Dial(clientID string) (*Client, error) {
	var lastErr error = errors.New("Discord is not running")
	for i := 0; i < 10; i++ {
		f, err := openPipe(fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i))
		if err != nil {
			continue
		}
		c, err := handshake(f, clientID)
		if err != nil {
			f.Close()
			lastErr = err
			continue
		}
		return c, nil
	}
	return nil, lastErr
}

// openPipe opens the pipe for overlapped I/O. With a synchronous handle,
// Windows serializes I/O on it, so the pending read of readLoop would block
// every write.
func openPipe(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func handshake(f *os.File, clientID string) (*Client, error) {
	c := &Client{f: f, pending: map[string]chan message{}, done: make(chan struct{})}
	if err := c.write(opHandshake, map[string]any{"v": 1, "client_id": clientID}); err != nil {
		return nil, err
	}
	op, body, err := readFrame(f)
	if err != nil {
		return nil, err
	}
	if op == opClose {
		return nil, fmt.Errorf("Discord rejected the handshake: %s", closeReason(body))
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m.Evt != "READY" {
		return nil, fmt.Errorf("unexpected handshake reply: %s", m.Evt)
	}
	var ready struct {
		User User `json:"user"`
	}
	_ = json.Unmarshal(m.Data, &ready)
	c.User = ready.User
	go c.readLoop()
	return c, nil
}

func closeReason(body []byte) string {
	var e struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
	}
	return string(body)
}

func readFrame(r io.Reader) (uint32, []byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := binary.LittleEndian.Uint32(hdr[0:4])
	n := binary.LittleEndian.Uint32(hdr[4:8])
	if n > 1<<20 {
		return 0, nil, fmt.Errorf("frame too large: %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return op, body, nil
}

func (c *Client) write(op uint32, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, op)
	binary.Write(&buf, binary.LittleEndian, uint32(len(body)))
	buf.Write(body)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.f.Write(buf.Bytes()); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

func (c *Client) fail(err error) {
	c.errOnce.Do(func() {
		c.err = err
		close(c.done)
	})
}

// Done is closed when the connection is lost.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) readLoop() {
	for {
		op, body, err := readFrame(c.f)
		if err != nil {
			c.fail(err)
			c.f.Close()
			return
		}
		switch op {
		case opPing:
			c.write(opPong, json.RawMessage(body))
		case opClose:
			c.fail(fmt.Errorf("Discord closed the connection: %s", closeReason(body)))
			c.f.Close()
			return
		case opFrame:
			var m message
			if json.Unmarshal(body, &m) != nil || m.Nonce == "" {
				continue
			}
			c.pmu.Lock()
			ch := c.pending[m.Nonce]
			delete(c.pending, m.Nonce)
			c.pmu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

func (c *Client) command(cmd string, args any) (message, error) {
	nonce := fmt.Sprint(c.nonce.Add(1))
	ch := make(chan message, 1)
	c.pmu.Lock()
	c.pending[nonce] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, nonce)
		c.pmu.Unlock()
	}()
	if err := c.write(opFrame, map[string]any{"cmd": cmd, "args": args, "nonce": nonce}); err != nil {
		return message{}, err
	}
	select {
	case m := <-ch:
		if m.Evt == "ERROR" {
			var e struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			json.Unmarshal(m.Data, &e)
			return m, fmt.Errorf("Discord error %d: %s", e.Code, e.Message)
		}
		return m, nil
	case <-c.done:
		return message{}, ErrClosed
	case <-time.After(10 * time.Second):
		return message{}, errors.New("timed out waiting for Discord")
	}
}

// SetActivity sets (or, with nil, clears) the user's activity.
func (c *Client) SetActivity(a *Activity) error {
	args := map[string]any{"pid": os.Getpid(), "activity": a}
	_, err := c.command("SET_ACTIVITY", args)
	return err
}

// Close asks Discord to close the connection; Discord then drops the
// presence and the read loop releases the pipe.
func (c *Client) Close() {
	select {
	case <-c.done:
		return
	default:
	}
	c.write(opClose, map[string]any{})
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}
