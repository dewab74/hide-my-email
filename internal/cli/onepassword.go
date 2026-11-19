package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// OpRunner runs the 1Password CLI with the given arguments and returns
// what it printed. It is a seam for tests; ExecOp is the real one.
type OpRunner func(ctx context.Context, args []string) (stdout []byte, stderr string, err error)

// ExecOp runs the `op` found on PATH. It is given an argument list, never
// a shell command line, and no stdin: `op item get` would otherwise read
// item names from it.
func ExecOp(ctx context.Context, args []string) ([]byte, string, error) {
	path, err := exec.LookPath("op")
	if err != nil {
		return nil, "", errors.New("the 1Password CLI (op) is not installed or not on PATH; see https://developer.1password.com/docs/cli/")
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- fixed program, arguments passed as a list
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}

// opField is one field of an item as `op item get --format json` prints it.
type opField struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Purpose string `json:"purpose"`
	Value   string `json:"value"`
}

func (f opField) is(name string) bool {
	return strings.EqualFold(f.Purpose, name) || strings.EqualFold(f.ID, name) || strings.EqualFold(f.Label, name)
}

// opCredentials reads the username and password of a 1Password login
// item. 1Password does its own unlocking (Touch ID, system prompt); hme
// only ever sees the two fields it asks for, and never prints them.
func (a *app) opCredentials(ctx context.Context, item, vault string) (username string, password []byte, err error) {
	item = strings.TrimSpace(item)
	if item == "" {
		return "", nil, usagef("--1password needs the name or ID of a 1Password item")
	}
	args := []string{"item", "get", "--fields", "label=username,label=password", "--reveal", "--format", "json"}
	if vault != "" {
		args = append(args, "--vault", vault)
	}
	// After "--" nothing is a flag, so an item name cannot be mistaken
	// for an option to op.
	args = append(args, "--", item)

	out, stderr, err := a.RunOp(ctx, args)
	defer clear(out)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", nil, err // op missing, or cancelled
		}
		// op's own message says what is wrong (not signed in, no such
		// item, several matches). Its stdout is never shown.
		detail := safe(firstLine(stderr))
		if detail == "" {
			detail = "op exited with an error"
		}
		return "", nil, fmt.Errorf("1Password: %s", strings.TrimPrefix(detail, "[ERROR] "))
	}

	// Several fields come back as an array, a single one as an object.
	var fields []opField
	if json.Unmarshal(out, &fields) != nil {
		var one opField
		if json.Unmarshal(out, &one) != nil {
			return "", nil, errors.New("1Password: could not read the item (unexpected output from op)")
		}
		fields = []opField{one}
	}
	for _, f := range fields {
		switch {
		case f.is("username") && username == "":
			username = strings.TrimSpace(f.Value)
		case f.is("password") && len(password) == 0:
			password = []byte(f.Value)
		}
	}
	if len(password) == 0 {
		return "", nil, fmt.Errorf("1Password: the item %q has no password", item)
	}
	return username, password, nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
