// Package cli is the hme command line: argument parsing, prompts and
// output. All iCloud logic lives in the icloud package.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dewab74/hide-my-email/internal/icloud"
	"github.com/dewab74/hide-my-email/internal/store"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
	ExitAuth  = 3 // not signed in, or the session expired
)

// Deps are the command line's connections to the outside world,
// replaceable in tests.
type Deps struct {
	In       io.Reader
	Out, Err io.Writer
	Prompter Prompter
	// Copy puts text on the system clipboard.
	Copy func(string) error
	// RunOp runs the 1Password CLI, for `hme login --1password`.
	RunOp OpRunner
	// ClientOptions are extra options for the iCloud client.
	ClientOptions []icloud.Option
	Version       string
}

type app struct {
	Deps
	json    bool
	debug   bool
	timeout time.Duration
}

// usageError marks a mistake in how the command was invoked.
type usageError struct{ error }

func usagef(format string, a ...any) error { return usageError{fmt.Errorf(format, a...)} }

// Run executes hme with args and returns the process exit code.
func Run(ctx context.Context, args []string, d Deps) int {
	a := &app{Deps: d}
	root := a.rootCmd()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	fmt.Fprintf(d.Err, "hme: %v\n", err)
	var ue usageError
	switch {
	case errors.As(err, &ue):
		return ExitUsage
	case icloud.IsAuthError(err), errors.Is(err, store.ErrKeyMissing):
		return ExitAuth
	}
	return ExitError
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hme",
		Short: "Manage iCloud Hide My Email addresses",
		Long: `hme creates and manages iCloud+ Hide My Email addresses.

Sign in once with 'hme login'; after that, 'hme generate -d "Some shop"'
creates an address and puts it on the clipboard.`,
		Version:       a.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q; see 'hme --help'", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.SetVersionTemplate("hme {{.Version}}\n")

	pf := root.PersistentFlags()
	pf.BoolVar(&a.json, "json", false, "write machine-readable JSON to stdout")
	pf.BoolVar(&a.debug, "debug", false, "trace requests to stderr (never headers or bodies)")
	pf.DurationVar(&a.timeout, "timeout", 30*time.Second, "per-request timeout")
	_ = root.RegisterFlagCompletionFunc("timeout", fixedChoices("10s", "30s", "1m"))

	root.AddGroup(
		&cobra.Group{ID: "alias", Title: "Addresses:"},
		&cobra.Group{ID: "account", Title: "Account:"},
	)
	for _, c := range []*cobra.Command{
		a.generateCmd(), a.listCmd(), a.showCmd(), a.copyCmd(), a.updateCmd(),
		a.deactivateCmd(), a.reactivateCmd(), a.deleteCmd(), a.forwardCmd(),
	} {
		c.GroupID = "alias"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{a.loginCmd(), a.logoutCmd(), a.statusCmd()} {
		c.GroupID = "account"
		root.AddCommand(c)
	}
	root.AddCommand(&cobra.Command{
		Use:               "version",
		Short:             "Print the version",
		Args:              exactArgs(0),
		ValidArgsFunction: noComplete,
		Run:               func(*cobra.Command, []string) { fmt.Fprintf(a.Out, "hme %s\n", a.Version) },
	})
	root.AddCommand(a.manCmd())
	return root
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usagef("'%s' takes %d argument(s), got %d", cmd.CommandPath(), n, len(args))
		}
		return nil
	}
}

func minArgs(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return usagef("'%s' needs %s", cmd.CommandPath(), what)
		}
		return nil
	}
}

func (a *app) store() (*store.Store, error) {
	dir, err := store.Dir()
	if err != nil {
		return nil, err
	}
	return store.New(dir, os.Getenv("HME_NO_KEYRING") != ""), nil
}

func (a *app) newClient(sess *icloud.Session) *icloud.Client {
	opts := []icloud.Option{icloud.WithTimeout(a.timeout)}
	if a.debug {
		opts = append(opts, icloud.WithDebug(a.Err))
	}
	return icloud.New(sess, append(opts, a.ClientOptions...)...)
}

// loadSession returns the stored session, or nil if there is none.
func (a *app) loadSession(st *store.Store) (sess *icloud.Session, encrypted bool, err error) {
	data, encrypted, err := st.Load()
	if err != nil || data == nil {
		return nil, false, err
	}
	sess = &icloud.Session{}
	if err := json.Unmarshal(data, sess); err != nil {
		return nil, false, errors.New("the stored session is unreadable; run 'hme login'")
	}
	return sess, encrypted, nil
}

func (a *app) saveSession(st *store.Store, c *icloud.Client) (encrypted bool, err error) {
	// The session is serialized on purpose: this is the one place it is
	// persisted, and the store encrypts it.
	data, err := json.Marshal(c.Session()) // #nosec G117
	if err != nil {
		return false, fmt.Errorf("encoding session: %w", err)
	}
	return st.Save(data)
}

// withClient runs fn with a signed-in client and persists whatever the
// session picked up along the way (rotated cookies, refreshed tokens),
// whether or not fn succeeded.
func (a *app) withClient(ctx context.Context, fn func(c *icloud.Client) error) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	sess, _, err := a.loadSession(st)
	if err != nil {
		return err
	}
	if sess == nil {
		return icloud.ErrNotLoggedIn
	}
	c := a.newClient(sess)
	err = c.Resume(ctx)
	if err == nil {
		err = fn(c)
	}
	if c.Changed() {
		if _, serr := a.saveSession(st, c); serr != nil && err == nil {
			err = serr
		}
	}
	return err
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
