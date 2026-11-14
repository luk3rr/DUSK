// Package ipc is the control channel between the dusk CLI and the running
// daemon: one JSON request line and one JSON response line per connection
// over a unix socket (ADR-4).
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Commands the daemon understands.
const (
	CmdStatus = "status"
	CmdPause  = "pause"
	CmdResume = "resume"
	CmdReload = "reload"
	CmdStop   = "stop"
)

const (
	maxLine     = 4096
	connTimeout = 5 * time.Second
	dialTimeout = 2 * time.Second
)

// Errors.
var (
	ErrAlreadyRunning = errors.New("another dusk instance is already running")
	ErrNoDaemon       = errors.New("dusk is not running")
)

// Request is a command sent to the daemon. It carries no paths or values.
type Request struct {
	Cmd string `json:"cmd"`
}

// MonitorStatus describes one monitor in a status reply.
type MonitorStatus struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Bus       int    `json:"bus"`
	Current   int    `json:"current"`
	Target    int    `json:"target"`
	Online    bool   `json:"online"`
	Fails     int    `json:"fails"`
	LastError string `json:"last_error"`
}

// Status is the payload of a status reply.
type Status struct {
	Paused   bool            `json:"paused"`
	Night    float64         `json:"night"`
	Monitors []MonitorStatus `json:"monitors"`
}

// Response is the daemon's reply. Status is set only for the status command.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	*Status
}

// Handler answers requests. The optional func it returns runs after the
// reply has been written (used by stop, so the client always gets its answer).
type Handler interface {
	Handle(ctx context.Context, req Request) (Response, func())
}

// SocketPath returns $XDG_RUNTIME_DIR/dusk.sock.
func SocketPath() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set; cannot place the control socket")
	}
	return filepath.Join(dir, "dusk.sock"), nil
}

// Listen creates the socket with mode 0600. It refuses to start when another
// daemon answers on the same path and removes a stale socket file.
func Listen(path string) (net.Listener, error) {
	if _, err := os.Stat(path); err == nil {
		if c, derr := net.DialTimeout("unix", path, time.Second); derr == nil {
			c.Close()
			return nil, ErrAlreadyRunning
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// Serve answers requests on ln until ctx is canceled, then closes ln (which
// also removes the socket file).
func Serve(ctx context.Context, ln net.Listener, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveConn(ctx, conn, h, log)
		}()
	}
}

func serveConn(ctx context.Context, conn net.Conn, h Handler, log *slog.Logger) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connTimeout))

	var resp Response
	var after func()
	line, err := bufio.NewReaderSize(io.LimitReader(conn, maxLine), maxLine).ReadBytes('\n')
	var req Request
	switch {
	case len(line) == 0 && err != nil:
		resp = Response{Error: "empty request"}
	case json.Unmarshal(line, &req) != nil:
		resp = Response{Error: "malformed request"}
	default:
		resp, after = h.Handle(ctx, req)
	}
	data, merr := json.Marshal(resp)
	if merr != nil {
		log.Error("encoding reply", "error", merr)
		return
	}
	if _, werr := conn.Write(append(data, '\n')); werr != nil {
		log.Warn("writing reply", "error", werr)
	}
	conn.Close()
	if after != nil {
		after()
	}
}

// Send sends one request to the daemon listening on path.
func Send(ctx context.Context, path string, req Request) (Response, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return Response{}, ErrNoDaemon
		}
		return Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connTimeout))
	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Response{}, fmt.Errorf("reading reply: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("decoding reply: %w", err)
	}
	return resp, nil
}
