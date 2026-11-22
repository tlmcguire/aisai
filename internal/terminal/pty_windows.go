//go:build windows

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A Windows terminal runs the shell on a pseudo-console (ConPTY), the API
// Windows 10 (version 1809) and later provide for exactly this: the shell
// believes it has a real console, and the app reads and writes two pipes.
// https://learn.microsoft.com/windows/console/creating-a-pseudoconsole-session
//
// creack/pty, which backs the macOS and Linux terminals, has no Windows
// support, so this uses the pseudo-console functions of golang.org/x/sys.

type windowsSession struct {
	hpc     windows.Handle // the pseudo-console
	process windows.Handle
	in      *os.File // the shell's input: what the user types
	out     *os.File // the shell's output
	once    sync.Once
	exited  atomic.Bool
}

func start(id string, opts Options, h Handlers) (session, error) {
	shell, args := windowsShell(opts.Shell)

	// Two pipes: the pseudo-console reads inR and writes outW; the app writes
	// inW and reads outR.
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("create the input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, fmt.Errorf("create the output pipe: %w", err)
	}
	closeAll := func(hs ...windows.Handle) {
		for _, x := range hs {
			if x != 0 {
				windows.CloseHandle(x)
			}
		}
	}

	var hpc windows.Handle
	size := windows.Coord{X: int16(opts.Cols), Y: int16(opts.Rows)}
	if err := windows.CreatePseudoConsole(size, inR, outW, 0, &hpc); err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, fmt.Errorf("the pseudo-console is not available (it needs Windows 10 version 1809 or later): %w", err)
	}
	// The pseudo-console holds its own copies of its two ends.
	closeAll(inR, outW)

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(inW, outR)
		return nil, err
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not a pointer to it.
	// Reading the handle's bits as a pointer avoids a uintptr-to-Pointer
	// conversion, which go vet rejects.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(inW, outR)
		return nil, err
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.ProcThreadAttributeList = attrs.List()

	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{shell}, args...)))
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(inW, outR)
		return nil, err
	}
	dir, err := windows.UTF16PtrFromString(opts.Cwd)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(inW, outR)
		return nil, err
	}
	env := windowsEnvBlock(environment())

	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, flags, &env[0], dir, &si.StartupInfo, &pi); err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(inW, outR)
		return nil, fmt.Errorf("start %s: %w", shell, err)
	}
	windows.CloseHandle(pi.Thread)

	s := &windowsSession{
		hpc:     hpc,
		process: pi.Process,
		in:      os.NewFile(uintptr(inW), "conpty-in"),
		out:     os.NewFile(uintptr(outR), "conpty-out"),
	}

	// Output is read until the pseudo-console closes; the process is waited
	// for separately. Closing the console after the shell exits is what ends
	// the output stream.
	var done sync.WaitGroup
	done.Add(2)
	go func() {
		defer done.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := s.out.Read(buf)
			if n > 0 && h.Data != nil {
				h.Data(id, append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
	code := 0
	go func() {
		defer done.Done()
		_, _ = windows.WaitForSingleObject(s.process, windows.INFINITE)
		var exit uint32
		if err := windows.GetExitCodeProcess(s.process, &exit); err == nil {
			code = int(exit)
		} else {
			code = -1
		}
		s.exited.Store(true)
		s.once.Do(func() { windows.ClosePseudoConsole(s.hpc) })
	}()
	go func() {
		done.Wait()
		windows.CloseHandle(s.process)
		_ = s.in.Close()
		_ = s.out.Close()
		if h.Exit != nil {
			h.Exit(id, code)
		}
	}()
	return s, nil
}

func (s *windowsSession) write(b []byte) error {
	_, err := s.in.Write(b)
	return err
}

func (s *windowsSession) resize(cols, rows uint16) error {
	return windows.ResizePseudoConsole(s.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (s *windowsSession) close() {
	// Closing the pseudo-console hangs up the shell, like closing a console
	// window. One that ignores it is ended shortly after.
	s.once.Do(func() { windows.ClosePseudoConsole(s.hpc) })
	process := s.process
	time.AfterFunc(2*time.Second, func() {
		if !s.exited.Load() {
			_ = windows.TerminateProcess(process, 1)
		}
	})
}

// windowsShell picks the shell: PowerShell if it is installed, else the
// command interpreter named by COMSPEC.
func windowsShell(override string) (string, []string) {
	if override != "" {
		return override, nil
	}
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, []string{"-NoLogo"}
		}
	}
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		return comspec, nil
	}
	return "cmd.exe", nil
}

// windowsEnvBlock builds the environment block CreateProcess takes: each
// "NAME=value" as UTF-16 ending in a NUL, and an extra NUL after the last.
func windowsEnvBlock(env []string) []uint16 {
	var block []uint16
	for _, kv := range env {
		if strings.ContainsRune(kv, 0) {
			continue
		}
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			continue
		}
		block = append(block, u...) // UTF16FromString already appends the NUL
	}
	return append(block, 0)
}
