package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// Prompter asks the person at the keyboard for input.
type Prompter interface {
	// Interactive reports whether there is a terminal to ask on.
	Interactive() bool
	// Line reads one line of visible input.
	Line(prompt string) (string, error)
	// Secret reads one line without echoing it.
	Secret(prompt string) ([]byte, error)
}

// TerminalPrompter prompts on the controlling terminal. It works even
// when stdin is a pipe (as with --password-stdin), because the prompts
// that remain, like the verification code, still need a person.
type TerminalPrompter struct {
	Err io.Writer
}

var errNoTerminal = errors.New("no terminal available to prompt on")

func openTerminal() (f *os.File, closeIt bool, err error) {
	// #nosec G115 -- file descriptors fit in an int
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return os.Stdin, false, nil
	}
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	f, err = os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil, false, errNoTerminal
	}
	return f, true, nil
}

func (p TerminalPrompter) Interactive() bool {
	f, closeIt, err := openTerminal()
	if err != nil {
		return false
	}
	if closeIt {
		_ = f.Close()
	}
	return true
}

func (p TerminalPrompter) Line(prompt string) (string, error) {
	f, closeIt, err := openTerminal()
	if err != nil {
		return "", err
	}
	if closeIt {
		defer func() { _ = f.Close() }()
	}
	fmt.Fprint(p.Err, prompt)
	// Byte at a time: reading ahead would swallow input meant for the
	// next prompt.
	var line []byte
	buf := make([]byte, 1)
	for len(line) < 4096 {
		n, err := f.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				break
			}
			return "", errors.New("input closed")
		}
	}
	return strings.TrimSpace(string(line)), nil
}

func (p TerminalPrompter) Secret(prompt string) ([]byte, error) {
	f, closeIt, err := openTerminal()
	if err != nil {
		return nil, err
	}
	if closeIt {
		defer func() { _ = f.Close() }()
	}
	fmt.Fprint(p.Err, prompt)
	// #nosec G115 -- file descriptors fit in an int
	secret, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(p.Err)
	if err != nil {
		return nil, errors.New("could not read hidden input")
	}
	return secret, nil
}
