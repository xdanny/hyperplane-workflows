// Package terminal bridges a browser xterm.js front-end to a real PTY on the
// host, so the Hyperplane web UI can drive the SDK conversation console
// and a login shell with full visibility into the local environment.
//
// Transport: a WebSocket per session.
//   - client → server: Binary frames are raw stdin bytes; Text frames are JSON
//     control messages ({"resize":{"cols":N,"rows":N}}).
//   - server → client: Binary frames are raw PTY output.
//
// Security: the WebSocket route is behind the same bearer-token auth as the rest
// of the API (see httpapi.authMiddleware). A PTY is a real shell on the box, so
// this must never be reachable unauthenticated from off-loopback.
package terminal

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Config describes how to launch the two terminal kinds.
type Config struct {
	// ShellCmd is the login shell launched for kind=shell.
	ShellCmd string
	// OrchestratorCmd is the interactive orchestrator launched for kind=orchestrator.
	OrchestratorCmd string
	// WorkDir is the working directory both kinds start in (the platform root, so
	// the orchestrator sees dags/, schemas/, runs/, the CLI, etc.).
	WorkDir string
	// Env is extra environment appended to the child process (e.g. so codex /
	// hyperplane CLI talk to the right API).
	Env []string
}

// controlMsg is the JSON shape of a client → server Text frame.
type controlMsg struct {
	Resize *struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	} `json:"resize,omitempty"`
}

// Serve runs one terminal session over an already-accepted WebSocket until the
// PTY closes, the socket closes, or ctx is cancelled. kind is "shell" or
// "orchestrator"; anything else defaults to shell.
func Serve(ctx context.Context, c *websocket.Conn, cfg Config, kind string, cols, rows uint16) error {
	cmdline := cfg.ShellCmd
	if kind == "orchestrator" && cfg.OrchestratorCmd != "" {
		cmdline = cfg.OrchestratorCmd
	}
	if cmdline == "" {
		cmdline = "/bin/sh"
	}

	cmd := exec.Command(cmdline)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	cmd.Env = append(os.Environ(), append([]string{"TERM=xterm-256color"}, cfg.Env...)...)

	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		// Surface the failure to the user's terminal rather than dying silently —
		// the most common case is a missing orchestrator binary (codex).
		_ = c.Write(ctx, websocket.MessageBinary,
			[]byte("\r\n\x1b[31mfailed to start '"+cmdline+"': "+err.Error()+"\x1b[0m\r\n"))
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	processDone := make(chan struct{})
	// The close handshake can wait for an unresponsive browser. Stop and reap
	// the process group first so cancellation never leaves tools running while
	// waiting for that acknowledgement. Defers run in reverse order.
	defer c.Close(websocket.StatusNormalClosure, "session ended")
	defer func() {
		cancel()
		// PTY starts a new session/process group. Kill its SDK/tools as well as
		// the console, and reap before releasing the application's session owner.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = ptmx.Close()
		<-processDone
	}()

	// PTY → client.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					cancel()
					return
				}
			}
			if rerr != nil {
				cancel()
				return
			}
		}
	}()

	// client → PTY.
	go func() {
		for {
			typ, data, rerr := c.Read(ctx)
			if rerr != nil {
				cancel()
				return
			}
			switch typ {
			case websocket.MessageText:
				var m controlMsg
				if json.Unmarshal(data, &m) == nil && m.Resize != nil {
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: m.Resize.Cols, Rows: m.Resize.Rows})
				}
			case websocket.MessageBinary:
				if _, werr := ptmx.Write(data); werr != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Reap the process so the session ends when the shell exits.
	go func() {
		_ = cmd.Wait()
		close(processDone)
		// Give the PTY-reader a moment to flush the final bytes.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	<-ctx.Done()
	return io.EOF
}
