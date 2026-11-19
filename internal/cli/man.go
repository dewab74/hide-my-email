package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// manCmd writes the manual pages. It is hidden: packagers and the
// release build use it, and the pages it writes are what users read.
func (a *app) manCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "man DIR",
		Short:  "Write the manual pages into DIR",
		Hidden: true,
		Long: `Write hme's manual pages, one per command, into DIR (created if
needed). To install them for yourself:

  hme man ~/.local/share/man/man1`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- man pages are public
				return fmt.Errorf("creating %s: %w", dir, err)
			}
			root := cmd.Root()
			root.DisableAutoGenTag = true // no timestamp footer: reproducible output
			header := &doc.GenManHeader{
				Title:   "HME",
				Section: "1",
				Source:  "hme " + a.Version,
				Manual:  "hme manual",
			}
			if err := doc.GenManTree(root, header, dir); err != nil {
				return fmt.Errorf("writing manual pages: %w", err)
			}
			fmt.Fprintf(a.Err, "Wrote the manual pages to %s\n", dir)
			return nil
		},
	}
}
