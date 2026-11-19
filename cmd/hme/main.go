// Command hme manages iCloud Hide My Email addresses.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"time"

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

// interruptGrace is how long a command gets to finish cleanly (cancel
// its request, save the session) after Ctrl-C before the process exits
// regardless. Network calls honour cancellation and return well inside
// it; a prompt waiting on the keyboard cannot be cancelled and does not.
const interruptGrace = time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	finished := make(chan struct{})
	go func() {
		<-ctx.Done()
		stop() // a second Ctrl-C now ends the process the default way
		select {
		case <-finished:
		case <-time.After(interruptGrace):
			cli.RestoreTerminal()
			fmt.Fprintln(os.Stderr, "\nhme: interrupted")
			os.Exit(130)
		}
	}()

	code := cli.Run(ctx, os.Args[1:], cli.Deps{
		In:       os.Stdin,
		Out:      os.Stdout,
		Err:      os.Stderr,
		Prompter: cli.TerminalPrompter{Err: os.Stderr},
		Copy:     clipboard.WriteAll,
		RunOp:    cli.ExecOp,
		Version:  buildVersion(),
	})
	if ctx.Err() != nil {
		code = 130
	}
	close(finished)
	stop()
	os.Exit(code)
}
