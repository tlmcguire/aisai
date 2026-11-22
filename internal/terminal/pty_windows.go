//go:build windows

package terminal

func start(string, Options, Handlers) (session, error) { return nil, ErrUnsupported }
