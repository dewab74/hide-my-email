package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	args := []string{"item", "get", "--fields=label=username,label=password", "--reveal", "--format=json"}
	if vault != "" {
		args = append(args, "--vault="+vault)
	}
	// After "--" nothing is a flag, so an item name cannot be mistaken
	// for an option to op.
	args = append(args, "--", item)

	out, err := a.op(ctx, args)
	defer clear(out)
	if err != nil {
		return "", nil, err
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

// op runs the 1Password CLI and turns its failures into errors fit to
// show: op's own message says what is wrong (not signed in, no such item
// or vault, several matches). Its stdout is never shown.
func (a *app) op(ctx context.Context, args []string) ([]byte, error) {
	out, stderr, err := a.RunOp(ctx, args)
	if err == nil {
		return out, nil
	}
	clear(out)
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return nil, err // op missing, or cancelled
	}
	detail := safe(firstLine(stderr))
	if detail == "" {
		detail = "op exited with an error"
	}
	return nil, fmt.Errorf("1Password: %s", strings.TrimPrefix(detail, "[ERROR] "))
}

// opReady confirms, before anything is created at Apple, that op is
// installed, unlocked, and can see the vault the item is meant for.
func (a *app) opReady(ctx context.Context, vault string) error {
	args := []string{"vault", "list", "--format=json"}
	if vault != "" {
		args = []string{"vault", "get", "--format=json", "--", vault}
	}
	out, err := a.op(ctx, args)
	clear(out)
	return err
}

// loginItem describes the 1Password login to create for a new address.
type loginItem struct {
	Title    string
	URL      string
	Username string
	Address  string // the Hide My Email address
	Note     string
	Vault    string
}

// args is the op command line that creates the item. It carries no
// secret: 1Password generates the password itself and keeps it.
func (it loginItem) args() []string {
	// Values are attached to their flags with "=", so a title or vault
	// that begins with a dash is still unmistakably a value.
	args := []string{"item", "create", "--category=login", "--title=" + it.Title, "--url=" + it.URL,
		"--generate-password", "--format=json"}
	if it.Vault != "" {
		args = append(args, "--vault="+it.Vault)
	}
	// Assignments follow "--", so none can be read as a flag.
	args = append(args, "--", "username="+it.Username)
	if it.Username != it.Address {
		// Signed up under another name: keep the address on the item too.
		args = append(args, "email[email]="+it.Address)
	}
	if it.Note != "" {
		args = append(args, "notesPlain="+it.Note)
	}
	return args
}

// itemRef identifies a created item. It is what hme reports; nothing
// else from op's answer is kept.
type itemRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Vault string `json:"vault"`
}

// opCreateLogin creates the login item and returns a reference to it.
func (a *app) opCreateLogin(ctx context.Context, it loginItem) (itemRef, error) {
	out, err := a.op(ctx, it.args())
	defer clear(out)
	if err != nil {
		return itemRef{}, err
	}
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Vault struct {
			Name string `json:"name"`
		} `json:"vault"`
	}
	if json.Unmarshal(out, &created) != nil || created.ID == "" {
		// op succeeded, so the item very likely exists; say so rather
		// than report a failure that invites a duplicate.
		return itemRef{Title: it.Title, Vault: it.Vault}, nil
	}
	return itemRef{ID: safe(created.ID), Title: safe(created.Title), Vault: safe(created.Vault.Name)}, nil
}

// siteURL validates the sign-up site's URL, accepting a bare host name.
func siteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil ||
		raw != safe(raw) || strings.ContainsAny(raw, " \t") {
		return "", usagef("--url needs a web address such as https://example.com")
	}
	return u.String(), nil
}

// shellQuote renders args as a command line that can be pasted into a
// POSIX shell.
func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-") == "" {
			quoted[i] = a
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
