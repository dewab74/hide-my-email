package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dewab74/hide-my-email/internal/icloud"
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

func (a *app) loginCmd() *cobra.Command {
	var appleID, opItem, opVault string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to iCloud",
		Long: `Sign in with your Apple ID, password and two-factor code.

The password is used once, in memory, to compute a proof for Apple's
SRP sign-in. It is never sent over the network and never stored. What
is stored is the resulting session, encrypted with a key kept in the
system credential store.

The Apple ID is taken from --apple-id, then $HME_APPLE_ID, then a
prompt. The password is prompted for without echo.

With --1password, both come from a login item in 1Password, through the
1Password CLI (op), which asks you to unlock it in its usual way:

  hme login --1password "Apple ID"
  hme login --1password "Apple ID" --vault Private

The item's username is used as the Apple ID unless --apple-id is given.

With --password-stdin the password is read from stdin, for any other
password manager or secret store:

  export HME_APPLE_ID=you@example.com
  op read "op://Private/Apple ID/password" | hme login --password-stdin

Either way, the two-factor code, if Apple asks for one, is still typed
at the terminal.`,
		Args:              exactArgs(0),
		ValidArgsFunction: noComplete,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case opItem != "" && passwordStdin:
				return usagef("--1password and --password-stdin exclude each other")
			case opVault != "" && opItem == "":
				return usagef("--vault only applies with --1password")
			case cmd.Flags().Changed("1password") && strings.TrimSpace(opItem) == "":
				return usagef("--1password needs the name or ID of a 1Password item")
			}
			return a.login(cmd.Context(), loginOptions{appleID: appleID, passwordStdin: passwordStdin, opItem: opItem, opVault: opVault})
		},
	}
	cmd.Flags().StringVarP(&appleID, "apple-id", "u", "", "Apple ID (email address)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	cmd.Flags().StringVar(&opItem, "1password", "", "take the Apple ID and password from this 1Password `item` (name or ID)")
	cmd.Flags().StringVar(&opVault, "vault", "", "with --1password: the vault to look in")
	for _, name := range []string{"apple-id", "1password", "vault"} {
		_ = cmd.RegisterFlagCompletionFunc(name, noComplete)
	}
	return cmd
}

type loginOptions struct {
	appleID       string
	passwordStdin bool
	opItem        string
	opVault       string
}

func (a *app) login(ctx context.Context, o loginOptions) error {
	appleID := o.appleID
	st, err := a.store()
	if err != nil {
		return err
	}
	// An unreadable old session is no obstacle to making a new one.
	prev, _, _ := a.loadSession(st)

	// The 1Password item is consulted first: it can supply the Apple ID
	// as well, and if it cannot be read there is no point asking for more.
	var password []byte
	defer func() { clear(password) }()
	if o.opItem != "" {
		var username string
		if username, password, err = a.opCredentials(ctx, o.opItem, o.opVault); err != nil {
			return err
		}
		if appleID == "" {
			appleID = username
		}
	}
	if appleID == "" {
		appleID = os.Getenv("HME_APPLE_ID")
	}
	if appleID == "" {
		if appleID, err = a.Prompter.Line("Apple ID: "); err != nil {
			return fmt.Errorf("no Apple ID given (use --apple-id): %w", err)
		}
	}
	appleID = strings.ToLower(strings.TrimSpace(appleID))
	if !strings.Contains(appleID, "@") {
		return usagef("%q does not look like an Apple ID (expected an email address)", appleID)
	}

	switch {
	case o.opItem != "":
		// already read
	case o.passwordStdin:
		// Typed at a terminal it would be echoed for anyone to read.
		if f, ok := a.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) { // #nosec G115 -- a file descriptor
			return usagef("--password-stdin expects the password piped in, but stdin is a terminal; drop the flag to be prompted without echo")
		}
		line, err := bufio.NewReader(io.LimitReader(a.In, 4096)).ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			clear(line)
			return errors.New("could not read the password from stdin")
		}
		// Trimmed in place: no second copy of the password is made.
		password = bytes.TrimRight(line, "\r\n")
	default:
		if password, err = a.Prompter.Secret("Password: "); err != nil {
			return fmt.Errorf("%w (or use --1password or --password-stdin)", err)
		}
	}
	if len(password) == 0 {
		return usagef("empty password")
	}

	// Keeping the trust token and client id from an earlier sign-in to
	// the same account lets Apple skip the second factor.
	sess := &icloud.Session{}
	if prev != nil && prev.AppleID == appleID {
		sess.TrustToken, sess.ClientID = prev.TrustToken, prev.ClientID
	}
	c := a.newClient(sess)
	if err := c.Login(ctx, appleID, password, a.askCode); err != nil {
		return err
	}

	// A new sign-in is the one place a plain file may be chosen, and it
	// is announced below.
	encrypted, err := a.saveSession(st, c, true)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"signedIn": true, "appleId": appleID, "encrypted": encrypted})
	}
	fmt.Fprintf(a.Err, "Signed in as %s.\n", appleID)
	if !encrypted {
		fmt.Fprintf(a.Err, "Warning: no system credential store is available, so the session is stored\nunencrypted (owner-only) at %s\n", st.Path())
	}
	return nil
}

func (a *app) askCode(_ context.Context, req icloud.TwoFactorRequest) (string, error) {
	if req.Method == "sms" {
		fmt.Fprintf(a.Err, "Apple sent a verification code by text message to %s.\n", req.Destination)
	} else {
		fmt.Fprintln(a.Err, "Apple sent a verification code to your trusted devices.")
	}
	for range 3 {
		code, err := a.Prompter.Line("Verification code: ")
		if err != nil {
			return "", fmt.Errorf("two-factor code needed: %w", err)
		}
		code = strings.NewReplacer(" ", "", "-", "").Replace(code)
		if sixDigits.MatchString(code) {
			return code, nil
		}
		fmt.Fprintln(a.Err, "The code is six digits.")
	}
	return "", errors.New("no valid verification code entered")
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the stored session",
		Long: `Delete the stored session and its encryption key from this machine.

This does not end the session on Apple's side. To do that, sign out of
all browsers under Account Settings on icloud.com.`,
		Args:              exactArgs(0),
		ValidArgsFunction: noComplete,
		RunE: func(*cobra.Command, []string) error {
			st, err := a.store()
			if err != nil {
				return err
			}
			if err := st.Delete(); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]any{"signedIn": false})
			}
			fmt.Fprintln(a.Err, "Signed out; the stored session was deleted.")
			return nil
		},
	}
}

func (a *app) statusCmd() *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"whoami"},
		Short:   "Show whether hme is signed in",
		Long: `Show the signed-in account and how the session is stored, and check
with Apple that the session still works (skip the check with --offline).
Exits with status 3 when not signed in.`,
		Args:              exactArgs(0),
		ValidArgsFunction: noComplete,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.store()
			if err != nil {
				return err
			}
			sess, encrypted, err := a.loadSession(st)
			if err != nil {
				return err
			}
			if sess == nil {
				return icloud.ErrNotLoggedIn
			}
			if !offline {
				c := a.newClient(sess)
				err := c.Refresh(cmd.Context())
				if c.Changed() {
					if _, serr := a.saveSession(st, c, !encrypted); serr != nil && err == nil {
						err = serr
					}
				}
				if err != nil {
					return err
				}
			}
			storage := "encrypted, key in the system credential store"
			if !encrypted {
				storage = "unencrypted owner-only file"
			}
			if a.json {
				return a.printJSON(map[string]any{
					"signedIn":       true,
					"appleId":        sess.AppleID,
					"verified":       !offline,
					"encrypted":      encrypted,
					"sessionFile":    st.Path(),
					"lastVerifiedAt": sess.ValidatedAt.UTC().Format(time.RFC3339),
				})
			}
			fmt.Fprintf(a.Out, "Signed in as:  %s\n", sess.AppleID)
			if offline {
				fmt.Fprintf(a.Out, "Session:       not checked (last verified %s)\n", sess.ValidatedAt.Local().Format("2006-01-02 15:04"))
			} else {
				fmt.Fprintln(a.Out, "Session:       valid")
			}
			fmt.Fprintf(a.Out, "Stored:        %s\n", storage)
			fmt.Fprintf(a.Out, "Session file:  %s\n", st.Path())
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "do not contact Apple")
	return cmd
}
