package cli

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dewab74/hide-my-email/internal/icloud"
)

// Shell completion. The scripts themselves come from cobra's
// `hme completion <shell>`; what is here teaches them hme's own values:
// addresses on the account, forwarding inboxes, and flag choices.

type completeFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// noComplete stops the shell from offering file names where none belong.
func noComplete(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completionTimeout bounds the lookup behind a Tab press.
const completionTimeout = 5 * time.Second

// listing fetches the account's addresses for completion. Completion
// must stay silent, so every failure (not signed in, offline, slow) just
// means no suggestions.
func (a *app) listing(ctx context.Context) *icloud.Listing {
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	var l *icloud.Listing
	_ = a.withClient(ctx, func(c *icloud.Client) error {
		var err error
		l, err = c.List(ctx)
		return err
	})
	return l
}

// completeAddresses suggests the account's addresses, each described by
// its label. max is how many addresses the command takes (0: any
// number); keep, if set, narrows them to the ones the command can act on.
func (a *app) completeAddresses(maxArgs int, keep func(icloud.Alias) bool) completeFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if maxArgs > 0 && len(args) >= maxArgs {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		l := a.listing(cmd.Context())
		if l == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, al := range l.Aliases {
			if (keep != nil && !keep(al)) || slices.Contains(args, al.Address) {
				continue
			}
			// The shell splits on whitespace, so only offer what is
			// certainly one clean word.
			addr := safe(al.Address)
			if addr != al.Address || strings.ContainsAny(addr, " \t") {
				continue
			}
			desc := safe(al.Label)
			if !al.Active {
				desc += " (inactive)"
			}
			out = append(out, addr+"\t"+desc)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func isActive(al icloud.Alias) bool   { return al.Active }
func isInactive(al icloud.Alias) bool { return !al.Active }

// completeInboxes suggests the inboxes `hme forward` can switch to.
func (a *app) completeInboxes(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	l := a.listing(cmd.Context())
	if l == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, o := range l.ForwardToOptions {
		if s := safe(o); s == o && !strings.ContainsAny(s, " \t") {
			if o == l.ForwardTo {
				s += "\tcurrent"
			}
			out = append(out, s)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// fixedChoices completes a flag from a fixed set of values.
func fixedChoices(choices ...string) completeFunc {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return choices, cobra.ShellCompDirectiveNoFileComp
	}
}
