package ipc_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dusk/internal/ipc"
)

// shortDir returns a short temp dir: unix socket paths are limited to ~108 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dusk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

type handlerFunc func(ctx context.Context, req ipc.Request) (ipc.Response, func())

func (f handlerFunc) Handle(ctx context.Context, req ipc.Request) (ipc.Response, func()) {
	return f(ctx, req)
}

func serve(t *testing.T, h ipc.Handler) (path string, stop func()) {
	t.Helper()
	path = filepath.Join(shortDir(t), "d.sock")
	ln, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ipc.Serve(ctx, ln, h, nil) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Serve: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("Serve did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return path, stop
}

func TestRoundTripAndAfterHook(t *testing.T) {
	var after atomic.Int32
	path, _ := serve(t, handlerFunc(func(_ context.Context, req ipc.Request) (ipc.Response, func()) {
		switch req.Cmd {
		case ipc.CmdStatus:
			return ipc.Response{OK: true, Status: &ipc.Status{Paused: true, Night: 0.5,
				Monitors: []ipc.MonitorStatus{{Name: "desk", Target: 30}}}}, nil
		case ipc.CmdStop:
			return ipc.Response{OK: true}, func() { after.Add(1) }
		}
		return ipc.Response{Error: "unknown command"}, nil
	}))

	resp, err := ipc.Send(t.Context(), path, ipc.Request{Cmd: ipc.CmdStatus})
	if err != nil || !resp.OK || resp.Status == nil || !resp.Paused || resp.Night != 0.5 || resp.Monitors[0].Name != "desk" {
		t.Fatalf("status = %+v, %v", resp, err)
	}
	if resp, _ := ipc.Send(t.Context(), path, ipc.Request{Cmd: "nope"}); resp.OK || resp.Error == "" {
		t.Errorf("unknown command = %+v", resp)
	}
	if resp, err := ipc.Send(t.Context(), path, ipc.Request{Cmd: ipc.CmdStop}); err != nil || !resp.OK {
		t.Fatalf("stop = %+v, %v", resp, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for after.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after.Load() != 1 {
		t.Errorf("after hook ran %d times; want 1", after.Load())
	}
}

func TestStatusFieldsAreFlatInJSON(t *testing.T) {
	path, _ := serve(t, handlerFunc(func(context.Context, ipc.Request) (ipc.Response, func()) {
		return ipc.Response{OK: true, Status: &ipc.Status{Monitors: []ipc.MonitorStatus{}}}, nil
	}))
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"cmd":"status"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(conn).ReadString('\n')
	for _, want := range []string{`"ok":true`, `"paused":false`, `"monitors":[]`} {
		if !strings.Contains(line, want) {
			t.Errorf("reply %q lacks %s", line, want)
		}
	}
}

func TestMalformedRequests(t *testing.T) {
	path, _ := serve(t, handlerFunc(func(context.Context, ipc.Request) (ipc.Response, func()) {
		t.Error("handler must not run for a malformed request")
		return ipc.Response{OK: true}, nil
	}))
	for name, payload := range map[string]string{
		"not json":  "hello\n",
		"oversized": strings.Repeat("x", 10000) + "\n",
		"empty":     "",
	} {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Logf("%s: write: %v", name, err) // the server may close early on oversized input
		}
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		line, _ := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		if !strings.Contains(line, `"ok":false`) || !strings.Contains(line, `"error"`) {
			t.Errorf("%s: reply %q", name, line)
		}
	}
}

func TestNoDaemon(t *testing.T) {
	missing := filepath.Join(shortDir(t), "none.sock")
	if _, err := ipc.Send(t.Context(), missing, ipc.Request{Cmd: ipc.CmdStatus}); !errors.Is(err, ipc.ErrNoDaemon) {
		t.Errorf("missing socket: %v", err)
	}
	// A stale socket file with nobody listening must also read as "not running".
	stale := filepath.Join(shortDir(t), "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := ipc.Send(t.Context(), stale, ipc.Request{Cmd: ipc.CmdStatus}); !errors.Is(err, ipc.ErrNoDaemon) {
		t.Errorf("stale socket: %v", err)
	}
}

func TestListenModeAndDuplicateAndStale(t *testing.T) {
	path, stop := serve(t, handlerFunc(func(context.Context, ipc.Request) (ipc.Response, func()) {
		return ipc.Response{OK: true}, nil
	}))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v; want 0600", info.Mode().Perm())
	}
	if _, err := ipc.Listen(path); !errors.Is(err, ipc.ErrAlreadyRunning) {
		t.Errorf("second Listen: %v", err)
	}
	stop()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file should be removed on stop: %v", err)
	}

	// A stale file (no listener) is replaced.
	stale := filepath.Join(shortDir(t), "stale.sock")
	l, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	ln, err := ipc.Listen(stale)
	if err != nil {
		t.Fatalf("stale socket should be recovered: %v", err)
	}
	ln.Close()
}

func TestSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	p, err := ipc.SocketPath()
	if err != nil || p != "/run/user/1000/dusk.sock" {
		t.Errorf("%q %v", p, err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := ipc.SocketPath(); err == nil {
		t.Error("expected an error without XDG_RUNTIME_DIR")
	}
}
