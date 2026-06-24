package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"

	"github.com/jphastings/charmera/internal/config"
)

// ErrNotRunning is returned when the daemon socket can't be reached.
var ErrNotRunning = errors.New("charmera daemon is not running")

// Client is a connection to a running daemon.
type Client struct {
	conn net.Conn
	dec  *json.Decoder
}

// Dial connects to the daemon's socket.
func Dial(cfg config.Config) (*Client, error) {
	conn, err := net.Dial("unix", cfg.SocketPath())
	if err != nil {
		return nil, ErrNotRunning
	}
	return &Client{conn: conn, dec: json.NewDecoder(bufio.NewReader(conn))}, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// ReadState returns the next state pushed by the daemon. On connect the daemon
// sends the current state first, so a single ReadState gives a snapshot.
func (c *Client) ReadState() (State, error) {
	var s State
	err := c.dec.Decode(&s)
	return s, err
}

// Send writes a command to the daemon.
func (c *Client) Send(command string) error {
	data, err := json.Marshal(Command{Command: command})
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(data, '\n'))
	return err
}

// Snapshot connects, reads one state, and disconnects.
func Snapshot(cfg config.Config) (State, error) {
	c, err := Dial(cfg)
	if err != nil {
		return State{}, err
	}
	defer c.Close()
	return c.ReadState()
}

// SendCommand connects, sends one command, waits for the resulting state, and
// disconnects. ctx bounds the wait for the acknowledging state.
func SendCommand(ctx context.Context, cfg config.Config, command string) (State, error) {
	c, err := Dial(cfg)
	if err != nil {
		return State{}, err
	}
	defer c.Close()

	if _, err := c.ReadState(); err != nil { // consume the initial snapshot
		return State{}, err
	}
	if err := c.Send(command); err != nil {
		return State{}, err
	}

	type result struct {
		s   State
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := c.ReadState()
		done <- result{s, err}
	}()

	select {
	case <-ctx.Done():
		return State{}, ctx.Err()
	case r := <-done:
		return r.s, r.err
	}
}
