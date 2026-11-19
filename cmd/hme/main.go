// Command hme manages iCloud Hide My Email addresses.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/atotto/clipboard"

	"github.com/dewab74/hide-my-email/internal/cli"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.Deps{
		In:       os.Stdin,
		Out:      os.Stdout,
		Err:      os.Stderr,
		Prompter: cli.TerminalPrompter{Err: os.Stderr},
		Copy:     clipboard.WriteAll,
		RunOp:    cli.ExecOp,
		Version:  buildVersion(),
	})
	stop()
	os.Exit(code)
}
