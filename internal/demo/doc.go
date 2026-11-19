// Package demo holds the two programs behind the recorded demo in the
// README (scripts/demo.sh): fakeapple, which serves the test fake of
// Apple's services on localhost, and hmedemo, which is hme wired to that
// fake, to a scripted 1Password CLI and keyboard, and to an in-memory
// clipboard and credential store. Neither is part of a release, and
// hmedemo refuses to talk to anything but localhost.
package demo
