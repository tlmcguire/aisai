//go:build !windows

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// unixSession is a shell on a pseudo-terminal: the shell sees a real
// terminal (so prompts, colours and line editing work), and the app reads
// and writes the other end. creack/pty opens it and starts the process:
// https://pkg.go.dev/github.com/creack/pty
type unixSession struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	once   sync.Once
	exited atomic.Bool
}

func start(id string, opts Options, h Handlers) (session, error) {
	shell := shellFor(opts.Shell)
	cmd := exec.Command(shell, "-l")
	cmd.Dir = opts.Cwd
	cmd.Env = environment()

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		return nil, err
	}
	s := &unixSession{cmd: cmd, ptmx: ptmx}

	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 && h.Data != nil {
				h.Data(id, append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				break
			}
		}
		code := 0
		err := cmd.Wait()
		s.exited.Store(true)
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else {
				code = -1
			}
		}
		s.once.Do(func() { _ = ptmx.Close() })
		if h.Exit != nil {
			h.Exit(id, code)
		}
	}()
	return s, nil
}

func (s *unixSession) write(b []byte) error {
	_, err := s.ptmx.Write(b)
	return err
}

func (s *unixSession) resize(cols, rows uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

func (s *unixSession) close() {
	// Hang up the shell the way a closed terminal window does, then make
	// sure it is gone.
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGHUP)
		// A shell that ignores the hang-up is killed shortly after.
		proc := s.cmd.Process
		time.AfterFunc(2*time.Second, func() {
			if !s.exited.Load() {
				_ = proc.Kill()
			}
		})
	}
	s.once.Do(func() { _ = s.ptmx.Close() })
}
