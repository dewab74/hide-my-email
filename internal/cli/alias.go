package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dewab74/hide-my-email/internal/icloud"
)

// aliasJSON is the stable JSON shape of an address. It is hme's own,
// not Apple's, so scripts do not break when Apple renames a field.
type aliasJSON struct {
	Address   string `json:"address"`
	Label     string `json:"label"`
	Note      string `json:"note"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"createdAt"`
	ForwardTo string `json:"forwardTo"`
	ID        string `json:"id"`
}

func toJSON(a icloud.Alias) aliasJSON {
	return aliasJSON{
		Address: a.Address, Label: a.Label, Note: a.Note, Active: a.Active,
		CreatedAt: a.Created().UTC().Format(time.RFC3339),
		ForwardTo: a.ForwardTo, ID: a.ID,
	}
}

func toJSONList(as []icloud.Alias) []aliasJSON {
	out := make([]aliasJSON, 0, len(as))
	for _, a := range as {
		out = append(out, toJSON(a))
	}
	return out
}

func status(a icloud.Alias) string {
	if a.Active {
		return "active"
	}
	return "inactive"
}

// safe prepares text that came from the network for the terminal.
func safe(s string) string { return icloud.Sanitize(s, 200) }

// copyOut puts text on the clipboard. Failure is a warning, not an
// error: on a headless machine the address is still on stdout.
func (a *app) copyOut(text string) bool {
	if err := a.Copy(text); err != nil {
		fmt.Fprintf(a.Err, "Note: could not copy to the clipboard (%v).\n", err)
		return false
	}
	return true
}

// find resolves an address (or alias id) the user typed. The match is
// exact: several of the commands that use it are destructive.
func find(l *icloud.Listing, arg string) (icloud.Alias, error) {
	want := strings.ToLower(strings.TrimSpace(arg))
	for _, al := range l.Aliases {
		if strings.ToLower(al.Address) == want || (al.ID != "" && al.ID == arg) {
			return al, nil
		}
	}
	return icloud.Alias{}, fmt.Errorf("no Hide My Email address %q on this account (see 'hme list')", arg)
}

func findAll(l *icloud.Listing, args []string) ([]icloud.Alias, error) {
	var out []icloud.Alias
	for _, arg := range args {
		al, err := find(l, arg)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(out, func(o icloud.Alias) bool { return o.ID == al.ID && o.Address == al.Address }) {
			out = append(out, al)
		}
	}
	return out, nil
}

func (a *app) generateCmd() *cobra.Command {
	var label, note, site, username, vault string
	var noCopy, onePassword bool
	cmd := &cobra.Command{
		Use:     "generate -d DESCRIPTION",
		Aliases: []string{"gen", "new"},
		Short:   "Create a new address and copy it to the clipboard",
		Long: `Create a new Hide My Email address labelled with DESCRIPTION.

The address alone is written to stdout, so it can be captured:

  addr=$(hme generate -d "Newsletter" --no-copy)

With --1password and --url, a login for that site is also saved in
1Password, through the 1Password CLI (op): titled DESCRIPTION, with the
new address as its username and a password that 1Password generates.
hme never sees that password. Use --username if the site wants a user
name that is not the email address; the address is then kept on the
item as its email field.`,
		Example: `  hme generate -d "Acme store"
  hme generate -d "Acme store" -n "ordered a kettle" --json
  hme generate -d "Acme store" --1password --url https://acme.example
  hme generate -d "Acme forum" --1password --url acme.example --username kettlefan --vault Private`,
		Args:              exactArgs(0),
		ValidArgsFunction: noComplete,
		RunE: func(cmd *cobra.Command, _ []string) error {
			label = strings.TrimSpace(label)
			if label == "" && !a.json && a.Prompter.Interactive() {
				var err error
				if label, err = a.Prompter.Line("Description: "); err != nil {
					return err
				}
			}
			if label == "" {
				return usagef("a description is required: hme generate -d \"what it is for\"")
			}
			note, username, vault = strings.TrimSpace(note), strings.TrimSpace(username), strings.TrimSpace(vault)
			switch {
			case !onePassword && (site != "" || username != "" || vault != ""):
				return usagef("--url, --username and --vault only apply with --1password")
			case onePassword && strings.TrimSpace(site) == "":
				return usagef("--1password needs the site: --url https://example.com")
			}
			if onePassword {
				var err error
				if site, err = siteURL(site); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			return a.withClient(ctx, func(c *icloud.Client) error {
				// Find out that 1Password is unusable before there is an
				// address to be left without its login.
				if onePassword {
					if err := a.opReady(ctx, vault); err != nil {
						return err
					}
				}
				al, err := c.Create(ctx, label, note)
				if err != nil {
					return err
				}
				copied := !noCopy && a.copyOut(al.Address)

				var saved *itemRef
				if onePassword {
					item := loginItem{Title: label, URL: site, Username: username, Address: al.Address, Note: note, Vault: vault}
					if item.Username == "" {
						item.Username = al.Address
					}
					ref, err := a.opCreateLogin(ctx, item)
					if err != nil {
						// The address exists and is not undone: say so, and
						// how to finish by hand.
						return fmt.Errorf("created %s, but saving the login failed: %w\nThe address was kept. To add the login yourself:\n  op %s",
							safe(al.Address), err, shellQuote(item.args()))
					}
					saved = &ref
				}

				if a.json {
					return a.printJSON(struct {
						aliasJSON
						Copied      bool     `json:"copied"`
						OnePassword *itemRef `json:"onePassword,omitempty"`
					}{toJSON(*al), copied, saved})
				}
				fmt.Fprintln(a.Out, safe(al.Address))
				if copied {
					fmt.Fprintln(a.Err, "Copied to the clipboard.")
				}
				if saved != nil {
					where := ""
					if saved.Vault != "" {
						where = " in the " + saved.Vault + " vault"
					}
					fmt.Fprintf(a.Err, "Saved a login for %s to 1Password as %q%s, with a generated password.\n", site, safe(label), where)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&label, "description", "d", "", "what the address is for (shown as its label in iCloud)")
	cmd.Flags().StringVarP(&note, "note", "n", "", "optional longer note")
	cmd.Flags().BoolVar(&noCopy, "no-copy", false, "do not copy the address to the clipboard")
	cmd.Flags().BoolVar(&onePassword, "1password", false, "also save a login for the site in 1Password, with a generated password")
	cmd.Flags().StringVar(&site, "url", "", "with --1password: the site's web address")
	cmd.Flags().StringVar(&username, "username", "", "with --1password: user name for the login (default: the new address)")
	cmd.Flags().StringVar(&vault, "vault", "", "with --1password: the vault to save in (default: your personal vault)")
	for _, name := range []string{"description", "note", "url", "username", "vault"} {
		_ = cmd.RegisterFlagCompletionFunc(name, noComplete)
	}
	return cmd
}

func (a *app) listCmd() *cobra.Command {
	var active, inactive, table, quiet bool
	var search, sortBy string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List addresses",
		Example: `  hme list --active
  hme list --search shop --json
  hme list --inactive --quiet | xargs hme delete --yes`,
		Args:              exactArgs(0),
		ValidArgsFunction: noComplete,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case active && inactive:
				return usagef("--active and --inactive exclude each other")
			case table && a.json, table && quiet, quiet && a.json:
				return usagef("choose one of --table, --json and --quiet")
			case !slices.Contains([]string{"created", "label", "address"}, sortBy):
				return usagef("--sort must be created, label or address")
			}
			return a.withClient(cmd.Context(), func(c *icloud.Client) error {
				l, err := c.List(cmd.Context())
				if err != nil {
					return err
				}
				needle := strings.ToLower(search)
				rows := slices.DeleteFunc(slices.Clone(l.Aliases), func(al icloud.Alias) bool {
					if (active && !al.Active) || (inactive && al.Active) {
						return true
					}
					return needle != "" && !strings.Contains(strings.ToLower(al.Address+"\n"+al.Label+"\n"+al.Note), needle)
				})
				slices.SortStableFunc(rows, func(x, y icloud.Alias) int {
					switch sortBy {
					case "label":
						return cmp.Compare(strings.ToLower(x.Label), strings.ToLower(y.Label))
					case "address":
						return cmp.Compare(x.Address, y.Address)
					}
					return cmp.Compare(y.CreateTimestamp, x.CreateTimestamp) // newest first
				})

				switch {
				case a.json:
					return a.printJSON(toJSONList(rows))
				case quiet:
					for _, al := range rows {
						fmt.Fprintln(a.Out, safe(al.Address))
					}
					return nil
				}
				if len(rows) == 0 {
					fmt.Fprintln(a.Err, "No addresses match.")
					return nil
				}
				a.printTable(rows)
				return nil
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&active, "active", false, "only active addresses")
	f.BoolVar(&inactive, "inactive", false, "only deactivated addresses")
	f.StringVarP(&search, "search", "s", "", "only addresses whose address, label or note contains `text`")
	f.StringVar(&sortBy, "sort", "created", "sort by created, label or address")
	f.BoolVar(&table, "table", false, "print a table (the default)")
	f.BoolVarP(&quiet, "quiet", "q", false, "print only the addresses, one per line")
	_ = cmd.RegisterFlagCompletionFunc("sort", fixedChoices("created\tnewest first", "label", "address"))
	_ = cmd.RegisterFlagCompletionFunc("search", noComplete)
	return cmd
}

func (a *app) printTable(rows []icloud.Alias) {
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tLABEL\tSTATUS\tCREATED\tNOTE")
	for _, al := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", safe(al.Address), icloud.Sanitize(al.Label, 40),
			status(al), al.Created().Local().Format("2006-01-02"), icloud.Sanitize(al.Note, 50))
	}
	_ = w.Flush()
}

func (a *app) printDetail(al icloud.Alias) {
	w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Address:\t%s\n", safe(al.Address))
	fmt.Fprintf(w, "Label:\t%s\n", safe(al.Label))
	fmt.Fprintf(w, "Note:\t%s\n", safe(al.Note))
	fmt.Fprintf(w, "Status:\t%s\n", status(al))
	fmt.Fprintf(w, "Created:\t%s\n", al.Created().Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "Forwards to:\t%s\n", safe(al.ForwardTo))
	fmt.Fprintf(w, "ID:\t%s\n", safe(al.ID))
	_ = w.Flush()
}

func (a *app) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "show ADDRESS",
		Short:             "Show one address in full",
		Args:              exactArgs(1),
		ValidArgsFunction: a.completeAddresses(1, nil),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withClient(cmd.Context(), func(c *icloud.Client) error {
				l, err := c.List(cmd.Context())
				if err != nil {
					return err
				}
				al, err := find(l, args[0])
				if err != nil {
					return err
				}
				if a.json {
					return a.printJSON(toJSON(al))
				}
				a.printDetail(al)
				return nil
			})
		},
	}
}

func (a *app) copyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "copy TEXT",
		Short: "Copy an existing address to the clipboard",
		Long: `Copy the one active address whose address or label contains TEXT.

  hme copy acme`,
		Args:              exactArgs(1),
		ValidArgsFunction: a.completeAddresses(1, isActive),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withClient(cmd.Context(), func(c *icloud.Client) error {
				l, err := c.List(cmd.Context())
				if err != nil {
					return err
				}
				needle := strings.ToLower(strings.TrimSpace(args[0]))
				var hits []icloud.Alias
				for _, al := range l.Aliases {
					if strings.ToLower(al.Address) == needle {
						hits = []icloud.Alias{al}
						break
					}
					if al.Active && strings.Contains(strings.ToLower(al.Address+"\n"+al.Label), needle) {
						hits = append(hits, al)
					}
				}
				switch len(hits) {
				case 0:
					return fmt.Errorf("no active address matches %q", args[0])
				case 1:
				default:
					fmt.Fprintf(a.Err, "%d addresses match %q; be more specific:\n", len(hits), args[0])
					for _, al := range hits {
						fmt.Fprintf(a.Err, "  %s  %s\n", safe(al.Address), safe(al.Label))
					}
					return fmt.Errorf("ambiguous match")
				}
				if err := a.Copy(hits[0].Address); err != nil {
					return fmt.Errorf("copying to the clipboard: %w", err)
				}
				if a.json {
					return a.printJSON(toJSON(hits[0]))
				}
				fmt.Fprintln(a.Out, safe(hits[0].Address))
				fmt.Fprintln(a.Err, "Copied to the clipboard.")
				return nil
			})
		},
	}
}

func (a *app) updateCmd() *cobra.Command {
	var label, note string
	cmd := &cobra.Command{
		Use:     "update ADDRESS [-d DESCRIPTION] [-n NOTE]",
		Aliases: []string{"edit"},
		Short:   "Change an address's description or note",
		Example: `  hme update abc-def.0x@icloud.com -d "Acme (old account)"
  hme update abc-def.0x@icloud.com -n ""     # clear the note`,
		Args:              exactArgs(1),
		ValidArgsFunction: a.completeAddresses(1, nil),
		RunE: func(cmd *cobra.Command, args []string) error {
			setLabel, setNote := cmd.Flags().Changed("description"), cmd.Flags().Changed("note")
			if !setLabel && !setNote {
				return usagef("nothing to change: give --description and/or --note")
			}
			if setLabel && strings.TrimSpace(label) == "" {
				return usagef("the description cannot be empty")
			}
			return a.withClient(cmd.Context(), func(c *icloud.Client) error {
				l, err := c.List(cmd.Context())
				if err != nil {
					return err
				}
				al, err := find(l, args[0])
				if err != nil {
					return err
				}
				if setLabel {
					al.Label = strings.TrimSpace(label)
				}
				if setNote {
					al.Note = strings.TrimSpace(note)
				}
				if err := c.Update(cmd.Context(), al.ID, al.Label, al.Note); err != nil {
					return err
				}
				if a.json {
					return a.printJSON(toJSON(al))
				}
				fmt.Fprintf(a.Err, "Updated %s.\n", safe(al.Address))
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&label, "description", "d", "", "new description (label)")
	cmd.Flags().StringVarP(&note, "note", "n", "", "new note; an empty string clears it")
	return cmd
}

// each applies one change to every named address.
func (a *app) each(ctx context.Context, args []string, done string,
	confirm func([]icloud.Alias) error, apply func(*icloud.Client, *icloud.Alias) error) error {
	return a.withClient(ctx, func(c *icloud.Client) error {
		l, err := c.List(ctx)
		if err != nil {
			return err
		}
		targets, err := findAll(l, args)
		if err != nil {
			return err
		}
		if confirm != nil {
			if err := confirm(targets); err != nil {
				return err
			}
		}
		for i := range targets {
			if err := apply(c, &targets[i]); err != nil {
				return fmt.Errorf("%s: %w", safe(targets[i].Address), err)
			}
			if !a.json {
				fmt.Fprintf(a.Err, "%s %s.\n", done, safe(targets[i].Address))
			}
		}
		if a.json {
			if done == "Deleted" {
				addrs := make([]string, 0, len(targets))
				for _, t := range targets {
					addrs = append(addrs, t.Address)
				}
				return a.printJSON(map[string]any{"deleted": addrs})
			}
			return a.printJSON(toJSONList(targets))
		}
		return nil
	})
}

func (a *app) deactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "deactivate ADDRESS...",
		Short:             "Stop addresses from forwarding mail (reversible)",
		Args:              minArgs(1, "at least one address"),
		ValidArgsFunction: a.completeAddresses(0, isActive),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.each(cmd.Context(), args, "Deactivated", nil, func(c *icloud.Client, al *icloud.Alias) error {
				if !al.Active {
					return nil
				}
				al.Active = false
				return c.Deactivate(cmd.Context(), al.ID)
			})
		},
	}
}

func (a *app) reactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "reactivate ADDRESS...",
		Aliases:           []string{"activate"},
		Short:             "Turn deactivated addresses back on",
		Args:              minArgs(1, "at least one address"),
		ValidArgsFunction: a.completeAddresses(0, isInactive),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.each(cmd.Context(), args, "Reactivated", nil, func(c *icloud.Client, al *icloud.Alias) error {
				if al.Active {
					return nil
				}
				al.Active = true
				return c.Reactivate(cmd.Context(), al.ID)
			})
		},
	}
}

func (a *app) deleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete ADDRESS...",
		Aliases: []string{"rm"},
		Short:   "Permanently delete addresses",
		Long: `Permanently delete addresses. Mail sent to a deleted address is lost
and the address cannot be recovered. To pause one instead, use
'hme deactivate'.

Asks for confirmation unless --yes is given.`,
		Args:              minArgs(1, "at least one address"),
		ValidArgsFunction: a.completeAddresses(0, nil),
		RunE: func(cmd *cobra.Command, args []string) error {
			confirm := func(targets []icloud.Alias) error {
				if yes {
					return nil
				}
				if !a.Prompter.Interactive() {
					return usagef("refusing to delete without confirmation; pass --yes")
				}
				fmt.Fprintln(a.Err, "This permanently deletes:")
				for _, t := range targets {
					fmt.Fprintf(a.Err, "  %s  %s\n", safe(t.Address), safe(t.Label))
				}
				answer, err := a.Prompter.Line("Delete? [y/N] ")
				if err != nil {
					return err
				}
				if answer = strings.ToLower(answer); answer != "y" && answer != "yes" {
					return fmt.Errorf("cancelled; nothing was deleted")
				}
				return nil
			}
			return a.each(cmd.Context(), args, "Deleted", confirm, func(c *icloud.Client, al *icloud.Alias) error {
				// Apple only deletes an address that is deactivated.
				if al.Active {
					if err := c.Deactivate(cmd.Context(), al.ID); err != nil {
						return err
					}
				}
				return c.Delete(cmd.Context(), al.ID)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) forwardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forward [EMAIL]",
		Short: "Show or change the inbox addresses forward to",
		Long: `Without an argument, show where Hide My Email forwards mail and which
inboxes can be chosen. With EMAIL, forward to that inbox instead; it
must be one of the listed choices.`,
		ValidArgsFunction: a.completeInboxes,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usagef("'%s' takes at most one argument", cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withClient(cmd.Context(), func(c *icloud.Client) error {
				l, err := c.List(cmd.Context())
				if err != nil {
					return err
				}
				if len(args) == 1 {
					i := slices.IndexFunc(l.ForwardToOptions, func(o string) bool { return strings.EqualFold(o, strings.TrimSpace(args[0])) })
					if i < 0 {
						return fmt.Errorf("%q is not one of this account's inboxes (see 'hme forward')", args[0])
					}
					if err := c.SetForwardTo(cmd.Context(), l.ForwardToOptions[i]); err != nil {
						return err
					}
					l.ForwardTo = l.ForwardToOptions[i]
				}
				if a.json {
					return a.printJSON(map[string]any{"forwardTo": l.ForwardTo, "choices": l.ForwardToOptions})
				}
				for _, o := range l.ForwardToOptions {
					mark := " "
					if o == l.ForwardTo {
						mark = "*"
					}
					fmt.Fprintf(a.Out, "%s %s\n", mark, safe(o))
				}
				return nil
			})
		},
	}
}
