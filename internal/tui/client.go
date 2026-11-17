// Filename: internal/tui/client.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

// Package tui is dusk's terminal interface (Bubble Tea). It talks to the
// running daemon over the control socket and edits the configuration file.
package tui

import (
	"context"
	"errors"
	"time"

	"dusk/internal/ipc"
)

const callTimeout = 2 * time.Second

// Client is what the interface needs from the daemon.
type Client interface {
	// Status returns the daemon's status, or ipc.ErrNoDaemon when it is not running.
	Status(ctx context.Context) (*ipc.Status, error)
	// Do sends a control command (pause, resume, reload, stop).
	Do(ctx context.Context, cmd string) error
}

// IPCClient implements Client over the daemon's unix socket.
type IPCClient struct{ Socket string }

// Status implements Client.
func (c IPCClient) Status(ctx context.Context) (*ipc.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := ipc.Send(ctx, c.Socket, ipc.Request{Cmd: ipc.CmdStatus})
	if err != nil {
		return nil, err
	}
	if !resp.OK || resp.Status == nil {
		return nil, errors.New(resp.Error)
	}
	return resp.Status, nil
}

// Do implements Client.
func (c IPCClient) Do(ctx context.Context, cmd string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := ipc.Send(ctx, c.Socket, ipc.Request{Cmd: cmd})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}
