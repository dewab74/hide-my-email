package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dewab74/hide-my-email/internal/cli"
	"github.com/dewab74/hide-my-email/internal/icloud"
	"github.com/dewab74/hide-my-email/internal/icloud/icloudtest"
)

// fakePrompter answers prompts from a script and records them.
type fakePrompter struct {
	interactive bool
	lines       []string
	secret      string
	asked       []string
}

func (p *fakePrompter) Interactive() bool { return p.interactive }

func (p *fakePrompter) Line(prompt string) (string, error) {
	p.asked = append(p.asked, prompt)
	if len(p.lines) == 0 {
		return "", errors.New("no terminal available to prompt on")
	}
	line := p.lines[0]
	p.lines = p.lines[1:]
	return line, nil
}

func (p *fakePrompter) Secret(prompt string) ([]byte, error) {
	p.asked = append(p.asked, prompt)
	return []byte(p.secret), nil
}

// env is one user's machine: a fake Apple, a config directory, a
// clipboard and a keyboard.
type env struct {
	t         *testing.T
	fake      *icloudtest.Fake
	dir       string
	prompter  *fakePrompter
	clipboard string
	copyErr   error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, fake: icloudtest.New(t), dir: filepath.Join(t.TempDir(), "hme"), prompter: &fakePrompter{}}
	t.Setenv("HME_CONFIG_DIR", e.dir)
	t.Setenv("HME_NO_KEYRING", "1") // never touch the real keychain from tests
	t.Setenv("HME_APPLE_ID", "")
	return e
}

type result struct {
	code        int
	out, errOut string
}

func (e *env) run(stdin string, args ...string) result {
	e.t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), args, cli.Deps{
		In: strings.NewReader(stdin), Out: &out, Err: &errOut,
		Prompter: e.prompter,
		Copy: func(s string) error {
			if e.copyErr != nil {
				return e.copyErr
			}
			e.clipboard = s
			return nil
		},
		ClientOptions: []icloud.Option{icloud.WithTestEndpoints(e.fake.AuthBase(), e.fake.SetupBase())},
		Version:       "test",
	})
	return result{code, out.String(), errOut.String()}
}

func (e *env) ok(args ...string) result {
	e.t.Helper()
	r := e.run("", args...)
	if r.code != 0 {
		e.t.Fatalf("hme %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.out, r.errOut)
	}
	return r
}

func (e *env) login() {
	e.t.Helper()
	e.prompter.lines = []string{icloudtest.AppleID, "123 456"}
	e.prompter.secret = icloudtest.Password
	e.ok("login")
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return v
}

type alias struct {
	Address, Label, Note, CreatedAt, ID string
	Active, Copied                      bool
}

func TestNotSignedIn(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"list"}, {"generate", "-d", "x"}, {"status"}, {"delete", "--yes", "a@icloud.com"}} {
		if r := e.run("", args...); r.code != cli.ExitAuth || !strings.Contains(r.errOut, "hme login") {
			t.Errorf("hme %v: exit %d, stderr %q", args, r.code, r.errOut)
		}
	}
}

func TestLoginStoresSessionButNeverThePassword(t *testing.T) {
	e := newEnv(t)
	e.prompter.lines = []string{icloudtest.AppleID, "12345", "123-456"} // first code is malformed
	e.prompter.secret = icloudtest.Password
	r := e.ok("login")
	if !strings.Contains(r.errOut, "Signed in as "+icloudtest.AppleID) || !strings.Contains(r.errOut, "unencrypted") {
		t.Errorf("stderr = %q", r.errOut)
	}
	if got := strings.Join(e.prompter.asked, "|"); got != "Apple ID: |Password: |Verification code: |Verification code: " {
		t.Errorf("prompts = %q", got)
	}

	raw, err := os.ReadFile(filepath.Join(e.dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(icloudtest.Password)) {
		t.Error("the session file contains the password")
	}
	var envl struct{ Data []byte }
	if err := json.Unmarshal(raw, &envl); err != nil || bytes.Contains(envl.Data, []byte(icloudtest.Password)) {
		t.Errorf("session payload: err=%v, or it contains the password", err)
	}

	st := decode[map[string]any](t, e.ok("status", "--json").out)
	if st["appleId"] != icloudtest.AppleID || st["signedIn"] != true || st["encrypted"] != false || st["verified"] != true {
		t.Errorf("status = %v", st)
	}
	if out := e.ok("status", "--offline").out; !strings.Contains(out, icloudtest.AppleID) || !strings.Contains(out, "not checked") {
		t.Errorf("status --offline = %q", out)
	}

	// Signing in again reuses the trust token: no code prompt.
	e.prompter.asked, e.prompter.lines = nil, nil
	e.ok("login", "--apple-id", icloudtest.AppleID)
	if got := strings.Join(e.prompter.asked, "|"); got != "Password: " {
		t.Errorf("prompts on second login = %q", got)
	}

	e.ok("logout")
	if _, err := os.Stat(filepath.Join(e.dir, "session.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the session file survived logout")
	}
	if r := e.run("", "list"); r.code != cli.ExitAuth {
		t.Errorf("list after logout: exit %d", r.code)
	}
}

func TestLoginPasswordFromStdin(t *testing.T) {
	e := newEnv(t)
	t.Setenv("HME_APPLE_ID", icloudtest.AppleID)
	e.prompter.lines = []string{icloudtest.Code}
	if r := e.run(icloudtest.Password+"\r\n", "login", "--password-stdin", "--json"); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	if got := strings.Join(e.prompter.asked, "|"); got != "Verification code: " {
		t.Errorf("prompts = %q", got)
	}
}

func TestLoginFailures(t *testing.T) {
	e := newEnv(t)
	e.prompter.secret = "wrong password"
	r := e.run("", "login", "-u", icloudtest.AppleID)
	if r.code != cli.ExitError || !strings.Contains(r.errOut, "incorrect Apple ID or password") || strings.Contains(r.errOut, "wrong password") {
		t.Errorf("exit %d, stderr %q", r.code, r.errOut)
	}
	if r := e.run("", "login", "-u", "not-an-email"); r.code != cli.ExitUsage {
		t.Errorf("bad Apple ID: exit %d", r.code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "session.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed login left a session file")
	}
}

func TestGenerate(t *testing.T) {
	e := newEnv(t)
	e.login()

	r := e.ok("generate", "-d", "Acme store")
	addr := strings.TrimSpace(r.out)
	if !strings.HasSuffix(addr, "@icloud.com") || strings.Contains(addr, "\n") {
		t.Fatalf("stdout = %q, want just the address", r.out)
	}
	if e.clipboard != addr || !strings.Contains(r.errOut, "Copied") {
		t.Errorf("clipboard = %q, stderr = %q", e.clipboard, r.errOut)
	}

	a := decode[alias](t, e.ok("generate", "--description", "Other", "--note", "n1", "--json").out)
	if a.Label != "Other" || a.Note != "n1" || !a.Active || !a.Copied || e.clipboard != a.Address || a.CreatedAt == "" {
		t.Errorf("generate --json = %+v, clipboard %q", a, e.clipboard)
	}

	e.clipboard = ""
	a = decode[alias](t, e.ok("gen", "-d", "Quiet", "--no-copy", "--json").out)
	if a.Copied || e.clipboard != "" {
		t.Error("--no-copy still copied")
	}

	// A broken clipboard is a note, not a failure.
	e.copyErr = errors.New("no display")
	r = e.ok("generate", "-d", "Headless")
	if !strings.HasSuffix(strings.TrimSpace(r.out), "@icloud.com") || !strings.Contains(r.errOut, "could not copy") {
		t.Errorf("headless generate: stdout %q, stderr %q", r.out, r.errOut)
	}
	e.copyErr = nil

	if r := e.run("", "generate"); r.code != cli.ExitUsage {
		t.Errorf("generate without a description: exit %d", r.code)
	}
	e.prompter.interactive, e.prompter.lines = true, []string{"Asked for"}
	e.ok("generate")
	as := e.fake.Aliases()
	if got := as[len(as)-1].Label; got != "Asked for" {
		t.Errorf("prompted label = %q", got)
	}
}

func TestList(t *testing.T) {
	e := newEnv(t)
	e.login()

	r := e.ok("list")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ADDRESS") || !strings.HasPrefix(lines[1], "news-letter.1b@icloud.com") {
		t.Errorf("table (newest first) =\n%s", r.out)
	}
	if !strings.Contains(lines[2], "inactive") || !strings.Contains(lines[2], "closed") {
		t.Errorf("row = %q", lines[2])
	}

	all := decode[[]alias](t, e.ok("list", "--json").out)
	act := decode[[]alias](t, e.ok("list", "--active", "--json").out)
	inact := decode[[]alias](t, e.ok("list", "--inactive", "--json").out)
	if len(all) != 2 || len(act) != 1 || !act[0].Active || len(inact) != 1 || inact[0].Active {
		t.Errorf("all=%d active=%+v inactive=%+v", len(all), act, inact)
	}
	if got := decode[[]alias](t, e.ok("list", "--search", "SHOP", "--json").out); len(got) != 1 || got[0].Label != "Old shop" {
		t.Errorf("search = %+v", got)
	}
	if got := decode[[]alias](t, e.ok("list", "--search", "zzz", "--json").out); got == nil || len(got) != 0 {
		t.Errorf("empty search should be [], got %+v", got)
	}
	if out := e.ok("list", "-q", "--sort", "address").out; out != "news-letter.1b@icloud.com\nold-shop.0a@icloud.com\n" {
		t.Errorf("quiet = %q", out)
	}
	e.ok("list", "--active", "--table")

	for _, bad := range [][]string{
		{"list", "--active", "--inactive"}, {"list", "--json", "--table"}, {"list", "--sort", "size"},
		{"list", "extra"}, {"list", "--nope"},
	} {
		if r := e.run("", bad...); r.code != cli.ExitUsage {
			t.Errorf("hme %v: exit %d, want usage error", bad, r.code)
		}
	}
}

func TestShowCopyUpdateForward(t *testing.T) {
	e := newEnv(t)
	e.login()
	const addr = "news-letter.1b@icloud.com"

	if out := e.ok("show", "NEWS-letter.1b@icloud.com").out; !strings.Contains(out, "Newsletter") || !strings.Contains(out, "active") {
		t.Errorf("show = %q", out)
	}
	if r := e.run("", "show", "nobody@icloud.com"); r.code != cli.ExitError || !strings.Contains(r.errOut, "no Hide My Email address") {
		t.Errorf("show unknown: %d %q", r.code, r.errOut)
	}

	if out := e.ok("copy", "newsl").out; strings.TrimSpace(out) != addr || e.clipboard != addr {
		t.Errorf("copy: out %q clipboard %q", out, e.clipboard)
	}
	if r := e.run("", "copy", "old shop"); r.code == 0 {
		t.Error("copy matched an inactive address by label")
	}
	e.ok("generate", "-d", "Newsletter two", "--no-copy")
	if r := e.run("", "copy", "newsletter"); r.code == 0 || !strings.Contains(r.errOut, "2 addresses match") {
		t.Errorf("ambiguous copy: %d %q", r.code, r.errOut)
	}

	a := decode[alias](t, e.ok("update", addr, "-d", "News", "--json").out)
	if a.Label != "News" {
		t.Errorf("update = %+v", a)
	}
	e.ok("update", addr, "-n", "weekly")
	e.ok("update", addr, "-n", "")
	for _, f := range e.fake.Aliases() {
		if f.Address == addr && (f.Label != "News" || f.Note != "") {
			t.Errorf("after updates: %+v", f)
		}
	}
	if r := e.run("", "update", addr); r.code != cli.ExitUsage {
		t.Errorf("update with nothing to change: exit %d", r.code)
	}

	if out := e.ok("forward").out; out != "* tester@icloud.com\n  tester@example.com\n" {
		t.Errorf("forward = %q", out)
	}
	fw := decode[map[string]any](t, e.ok("forward", "Tester@Example.com", "--json").out)
	if fw["forwardTo"] != "tester@example.com" {
		t.Errorf("forward set = %v", fw)
	}
	if r := e.run("", "forward", "stranger@example.com"); r.code != cli.ExitError {
		t.Errorf("forward to an unknown inbox: exit %d", r.code)
	}
}

func TestDeactivateReactivateDelete(t *testing.T) {
	e := newEnv(t)
	e.login()
	const addr = "news-letter.1b@icloud.com"
	state := func() (found, active bool) {
		for _, f := range e.fake.Aliases() {
			if f.Address == addr {
				return true, f.Active
			}
		}
		return false, false
	}

	e.ok("deactivate", addr)
	if _, active := state(); active {
		t.Fatal("still active after deactivate")
	}
	got := decode[[]alias](t, e.ok("reactivate", addr, "--json").out)
	if _, active := state(); !active || len(got) != 1 || !got[0].Active {
		t.Fatalf("reactivate: %+v", got)
	}

	// Deleting needs a yes.
	if r := e.run("", "delete", addr); r.code != cli.ExitUsage || !strings.Contains(r.errOut, "--yes") {
		t.Errorf("non-interactive delete without --yes: %d %q", r.code, r.errOut)
	}
	e.prompter.interactive, e.prompter.lines = true, []string{"n"}
	if r := e.run("", "delete", addr); r.code != cli.ExitError || !strings.Contains(r.errOut, "nothing was deleted") {
		t.Errorf("declined delete: %d %q", r.code, r.errOut)
	}
	if found, _ := state(); !found {
		t.Fatal("deleted despite a no")
	}
	if r := e.run("", "delete", "--yes", addr, "missing@icloud.com"); r.code == 0 {
		t.Error("delete with an unknown address succeeded")
	}
	if found, _ := state(); !found {
		t.Fatal("a failed batch deleted something")
	}

	// An active address is deactivated first, then deleted.
	e.prompter.lines = []string{"y"}
	r := e.ok("delete", addr)
	if found, _ := state(); found || !strings.Contains(r.errOut, "Newsletter") || !strings.Contains(r.errOut, "Deleted "+addr) {
		t.Errorf("delete: found=%v stderr=%q", found, r.errOut)
	}
	del := decode[map[string][]string](t, e.ok("rm", "-y", "old-shop.0a@icloud.com", "--json").out)
	if len(del["deleted"]) != 1 || len(e.fake.Aliases()) != 0 {
		t.Errorf("delete --json = %v, left %d", del, len(e.fake.Aliases()))
	}
}

func TestSessionSurvivesCookieExpiryAndRevocation(t *testing.T) {
	e := newEnv(t)
	e.login()
	before, _ := os.ReadFile(filepath.Join(e.dir, "session.json"))

	e.fake.ExpireCookies()
	e.ok("list")
	after, _ := os.ReadFile(filepath.Join(e.dir, "session.json"))
	if bytes.Equal(before, after) {
		t.Error("the refreshed cookies were not saved")
	}
	e.ok("list") // and the saved ones work

	e.fake.RevokeSession()
	if r := e.run("", "list"); r.code != cli.ExitAuth || !strings.Contains(r.errOut, "hme login") {
		t.Errorf("revoked session: %d %q", r.code, r.errOut)
	}
}

func TestOutputIsSafeForTerminals(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.ok("generate", "-d", "evil\x1b[2J\x1b]0;pwned\x07label", "--no-copy")
	if out := e.ok("list").out; strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("table contains escape sequences: %q", out)
	}
}

func TestVersionAndHelp(t *testing.T) {
	e := newEnv(t)
	if out := e.ok("version").out; out != "hme test\n" {
		t.Errorf("version = %q", out)
	}
	if out := e.ok("--version").out; out != "hme test\n" {
		t.Errorf("--version = %q", out)
	}
	out := e.ok("--help").out
	for _, verb := range []string{"generate", "list", "delete", "deactivate", "reactivate", "update", "show", "copy", "forward", "login", "logout", "status"} {
		if !strings.Contains(out, "\n  "+verb+" ") {
			t.Errorf("help does not list %q", verb)
		}
	}
	if r := e.run("", "frobnicate"); r.code != cli.ExitUsage {
		t.Error("an unknown command succeeded")
	}
}
