// Command hmedemo is hme for the recorded demo: the real command line,
// wired to the fake Apple on localhost and to scripted stand-ins for the
// 1Password CLI, the keyboard, the clipboard and the credential store.
// See package demo.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/dewab74/hide-my-email/internal/cli"
	"github.com/dewab74/hide-my-email/internal/icloud"
	"github.com/dewab74/hide-my-email/internal/icloud/icloudtest"
	"github.com/dewab74/hide-my-email/internal/store"
)

func main() {
	base := os.Getenv("HME_DEMO_URL")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		fmt.Fprintln(os.Stderr, "hmedemo: HME_DEMO_URL must be the fakeapple address on 127.0.0.1")
		os.Exit(2)
	}
	dir, err := store.Dir()
	if err != nil || os.Getenv("HME_CONFIG_DIR") == "" {
		fmt.Fprintln(os.Stderr, "hmedemo: set HME_CONFIG_DIR to a scratch directory")
		os.Exit(2)
	}

	// An in-memory credential store, carried between runs in a file in the
	// scratch directory, so the demo never touches the real keychain.
	keyring.MockInit()
	service, user := store.KeyringEntry(dir)
	keyFile := filepath.Join(dir, ".demo-keyring")
	if b, err := os.ReadFile(keyFile); err == nil { // #nosec G304 -- scratch directory
		_ = keyring.Set(service, user, string(b))
	}

	code := cli.Run(context.Background(), os.Args[1:], cli.Deps{
		In:       os.Stdin,
		Out:      os.Stdout,
		Err:      os.Stderr,
		Prompter: typist{},
		Copy:     func(string) error { return nil },
		RunOp:    fakeOp,
		ClientOptions: []icloud.Option{
			icloud.WithTestEndpoints(base+"/auth", base+"/setup"),
		},
		Version: envOr("HME_DEMO_VERSION", "dev"),
	})

	if k, err := keyring.Get(service, user); err == nil {
		_ = os.WriteFile(keyFile, []byte(k), 0o600)
	} else {
		_ = os.Remove(keyFile)
	}
	os.Exit(code)
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// typist answers prompts as if someone were typing at the keyboard.
type typist struct{}

func (typist) Interactive() bool { return true }

func (typist) Line(prompt string) (string, error) {
	answer := ""
	switch {
	case strings.HasPrefix(prompt, "Verification code"):
		answer = icloudtest.Code
	case strings.Contains(prompt, "[y/N]"):
		answer = "y"
	default:
		return "", errors.New("hmedemo: no scripted answer for " + prompt)
	}
	fmt.Fprint(os.Stderr, prompt)
	time.Sleep(900 * time.Millisecond)
	for _, r := range answer {
		fmt.Fprint(os.Stderr, string(r))
		time.Sleep(110 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	fmt.Fprintln(os.Stderr)
	return answer, nil
}

func (typist) Secret(string) ([]byte, error) { return []byte(icloudtest.Password), nil }

// fakeOp stands in for the 1Password CLI.
func fakeOp(_ context.Context, args []string) ([]byte, string, error) {
	time.Sleep(400 * time.Millisecond) // 1Password unlocking
	if len(args) < 2 {
		return nil, "", errors.New("hmedemo: unexpected op call")
	}
	switch args[0] + " " + args[1] {
	case "vault list", "vault get":
		return []byte(`[{"id":"v1","name":"Private"}]`), "", nil
	case "item get":
		out, err := json.Marshal([]map[string]string{
			{"id": "username", "purpose": "USERNAME", "label": "username", "value": icloudtest.AppleID},
			{"id": "password", "purpose": "PASSWORD", "label": "password", "value": icloudtest.Password},
		})
		return out, "", err
	case "item create":
		title, vault := "", "Private"
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "--title="); ok {
				title = v
			}
			if v, ok := strings.CutPrefix(a, "--vault="); ok {
				vault = v
			}
		}
		out, err := json.Marshal(map[string]any{"id": "x4kq2demoitem", "title": title, "vault": map[string]string{"name": vault}})
		return out, "", err
	}
	return nil, "", errors.New("hmedemo: unexpected op call " + strings.Join(args, " "))
}
