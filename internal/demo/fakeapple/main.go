// Command fakeapple serves the fake Apple used by the tests on a local
// address, with demo data, until interrupted. See package demo.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/dewab74/hide-my-email/internal/icloud/icloudtest"
)

// Addresses handed out by generate, in order. Invented, in Apple's style.
var fresh = []string{
	"plover-tidbit.4x@icloud.com",
	"amber-sprocket.2q@icloud.com",
	"gravel-lantern.7m@icloud.com",
	"velvet-oarlock.9c@icloud.com",
}

func main() {
	addr := flag.String("listen", "127.0.0.1:8765", "address to listen on")
	flag.Parse()

	f, err := icloudtest.Listen(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeapple:", err)
		os.Exit(1)
	}
	defer f.Close()

	day := func(n int) int64 { return time.Now().AddDate(0, 0, -n).UnixMilli() }
	f.SetAliases([]icloudtest.Alias{
		{ID: "a1", Address: "quiet-meadow.4k@icloud.com", Label: "Grocery delivery", Active: true, ForwardTo: "tester@icloud.com", Created: day(3)},
		{ID: "a2", Address: "copper-kettle.8r@icloud.com", Label: "Newsletter", Note: "weekly digest", Active: true, ForwardTo: "tester@icloud.com", Created: day(40)},
		{ID: "a3", Address: "nimble-ferry.1z@icloud.com", Label: "Airline loyalty", Active: true, ForwardTo: "tester@icloud.com", Created: day(95)},
		{ID: "a4", Address: "dusty-almanac.5p@icloud.com", Label: "Old forum", Note: "spam after breach", Active: false, ForwardTo: "tester@icloud.com", Created: day(400)},
	})
	var mu sync.Mutex
	next := 0
	f.NewAddress = func() string {
		mu.Lock()
		defer mu.Unlock()
		a := fresh[next%len(fresh)]
		next++
		return a
	}
	f.Now = time.Now

	fmt.Println(f.URL)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}
