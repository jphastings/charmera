package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
)

// serve listens on the Unix-domain socket until ctx is cancelled. Each
// connection is fed the current state immediately and every subsequent change,
// and may send command lines (pause/resume/toggle) back.
func (d *Daemon) serve(ctx context.Context) error {
	sock := d.opts.Config.SocketPath()
	// A stale socket from a previous run would block Listen.
	_ = os.Remove(sock)

	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer func() {
		ln.Close()
		_ = os.Remove(sock)
	}()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // shutting down
			}
			return err
		}
		go d.handleConn(ctx, conn)
	}
}

func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	states, unsubscribe := d.subscribe()
	defer unsubscribe()

	// Read commands from the client in the background; close the connection's
	// read side when the client hangs up so the writer loop can exit.
	go d.readCommands(conn)

	enc := json.NewEncoder(conn)
	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-states:
			if !ok {
				return
			}
			if err := enc.Encode(s); err != nil {
				return // client gone
			}
		}
	}
}

func (d *Daemon) readCommands(conn net.Conn) {
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var cmd Command
		if json.Unmarshal(sc.Bytes(), &cmd) != nil {
			continue
		}
		switch cmd.Command {
		case CmdPause:
			d.SetPaused(true)
		case CmdResume:
			d.SetPaused(false)
		case CmdToggle:
			d.Toggle()
		}
	}
}
