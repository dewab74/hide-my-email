# hme

[![CI](https://github.com/dewab74/hide-my-email/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/dewab74/hide-my-email/actions/workflows/ci.yml)
[![Release](https://github.com/dewab74/hide-my-email/actions/workflows/release.yml/badge.svg)](https://github.com/dewab74/hide-my-email/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/dewab74/hide-my-email)](https://github.com/dewab74/hide-my-email/releases/latest)
[![License: MIT](https://img.shields.io/github/license/dewab74/hide-my-email)](LICENSE)

A command-line tool for iCloud+ **Hide My Email**. Create a throwaway address
in one command, have it land on your clipboard, and list, relabel, pause or
delete the ones you already have.

```console
$ hme generate -d "Acme store"
plover-tidbit.4x@icloud.com
Copied to the clipboard.
```

`hme` is a single static binary for macOS, Linux and Windows. It signs in to
iCloud itself (Apple ID, password, two-factor code), so it needs no browser,
no cookie export and no other runtime.

> **Status: early, and vibe-coded.** This project was written almost
> entirely by Claude (Anthropic's AI model, via Claude Code) under my
> direction, including the sign-in and cryptography code. It has an
> extensive test suite that runs against a protocol-faithful fake of Apple's
> services, but it has had no independent security review, Apple publishes
> no API for Hide My Email, and this release has not yet been exercised
> against the live service. Read the code before you trust it with your
> Apple Account, and expect to report a bug or two. See [TODO.md](TODO.md).

## Why this exists

I wrote `hme` to scratch my own itch: I wanted Hide My Email addresses from
a terminal, on my own machines, without clicking through Settings. It is
shared in case it is useful to someone else, not as a supported product.

In part, that itch exists because 1Password has no Hide My Email
integration: it cannot create an iCloud address for you when you sign up
somewhere, so doing that means a detour through Settings or icloud.com. `hme` closes that gap from
the command line, and [pairing it with 1Password](TODO.md#1password-integration-04)
is planned. If 1Password adds Hide My Email support itself, this project
may become unnecessary.

## Install

### Homebrew (macOS and Linux)

```bash
brew tap dewab74/hide-my-email https://github.com/dewab74/hide-my-email
```

```bash
brew install hme
```

This installs the prebuilt binary for your platform, verified against its
published SHA-256, along with shell completions for bash, zsh and fish.
Later:

```bash
brew upgrade hme
```

```bash
brew uninstall hme && brew untap dewab74/hide-my-email
```

### Prebuilt binaries

Every [release](https://github.com/dewab74/hide-my-email/releases) has
archives for macOS, Linux and Windows on amd64 and arm64, plus
`checksums.txt`. Download the archive for your platform and `checksums.txt`
into the same directory, then verify before unpacking:

```bash
shasum -a 256 --check --ignore-missing checksums.txt
```

Releases also carry a build provenance attestation, which proves the archive
was built by this repository's release workflow:

```bash
gh attestation verify hme_0.1.0_darwin_arm64.tar.gz --repo dewab74/hide-my-email
```

Unpack it and put `hme` (`hme.exe` on Windows) somewhere on your `PATH`.

### From source

With Go 1.27 or newer:

```bash
go install github.com/dewab74/hide-my-email/cmd/hme@latest
```

Or from a clone:

```bash
make build      # produces ./hme
```

## Quick start

```bash
hme login                          # Apple ID, password, 2FA code; once
hme generate -d "Acme store"       # new address, copied to the clipboard
hme list --active                  # what is in use
hme deactivate plover-tidbit.4x@icloud.com
hme delete plover-tidbit.4x@icloud.com
```

You need an iCloud+ subscription (Hide My Email is part of it) and, on the
account, **Access iCloud Data on the Web** turned on (called *Allow Data
Access* on newer systems; it is off when Advanced Data Protection is on).

## Commands

| Command | What it does |
| --- | --- |
| `hme generate -d DESC [-n NOTE] [--no-copy]` | Create an address labelled `DESC` and copy it to the clipboard. Aliases: `gen`, `new`. |
| `hme list [--active \| --inactive] [-s TEXT] [--sort created\|label\|address] [-q]` | List addresses, newest first. `-q` prints addresses only. Alias: `ls`. |
| `hme show ADDRESS` | Everything about one address. |
| `hme copy TEXT` | Copy the one active address whose address or label contains `TEXT`. |
| `hme update ADDRESS [-d DESC] [-n NOTE]` | Change the description or note. `-n ""` clears the note. Alias: `edit`. |
| `hme deactivate ADDRESS...` | Stop forwarding mail. Reversible. |
| `hme reactivate ADDRESS...` | Turn a deactivated address back on. Alias: `activate`. |
| `hme delete ADDRESS... [--yes]` | Delete permanently. Asks first unless `--yes`. Alias: `rm`. |
| `hme forward [EMAIL]` | Show, or change, the inbox addresses forward to. |
| `hme login [-u APPLE_ID] [--password-stdin]` | Sign in. |
| `hme logout` | Delete the stored session and its key from this machine. |
| `hme status [--offline]` | Who is signed in, and whether the session still works. Alias: `whoami`. |
| `hme completion bash\|zsh\|fish\|powershell` | Shell completion script. |

Global flags: `--json` (machine-readable output on stdout), `--debug` (trace
requests on stderr), `--timeout 30s`. Every command has `--help`.

### Scripting

Results go to stdout and everything else (prompts, confirmations, warnings) to
stderr, so output can be captured or piped as is:

```bash
addr=$(hme generate -d "CI signup" --no-copy)

hme list --json | jq -r '.[] | select(.active) | .address'

hme list --inactive -q | xargs hme delete --yes
```

With `--json`, an address looks like this, and the field names are `hme`'s
own, so they stay put if Apple renames theirs:

```json
{
  "address": "plover-tidbit.4x@icloud.com",
  "label": "Acme store",
  "note": "",
  "active": true,
  "createdAt": "2026-11-18T20:51:00Z",
  "forwardTo": "you@icloud.com",
  "id": "…"
}
```

`generate --json` adds `"copied": true|false`.

Exit codes: `0` success, `1` failure, `2` usage error, `3` not signed in or
the session expired (run `hme login`).

### Signing in non-interactively

The password can come from a password manager instead of the keyboard:

```bash
op read "op://Private/Apple/password" | hme login -u you@example.com --password-stdin
```

The two-factor code is still asked for on the terminal. After the first
sign-in Apple trusts the installation, so later logins usually skip the code.

### Environment

| Variable | Meaning |
| --- | --- |
| `HME_APPLE_ID` | Default Apple ID for `hme login`. |
| `HME_CONFIG_DIR` | Where the session file lives. Default: the per-user config directory (`~/Library/Application Support/hme`, `~/.config/hme`, `%AppData%\hme`). |
| `HME_NO_KEYRING` | If set, never use the system credential store (see below). |

There is deliberately no environment variable for the password.

### Clipboard

`generate` and `copy` use `pbcopy` on macOS, the Windows clipboard API, and on
Linux whichever of `wl-copy`, `xclip` or `xsel` is installed. If none is
available (a headless box, say) the address is still printed and `hme` just
says it could not copy.

## Security

The short version: your password never leaves your machine and is never
written anywhere; what is kept is a session, encrypted at rest.

- **No password on the wire or on disk.** Sign-in uses Apple's SRP protocol:
  the password is used once, in memory, to compute a proof, and the buffer is
  wiped afterwards.
- **The session is encrypted.** It is stored AES-256-GCM encrypted, with the
  key held in the macOS Keychain, Windows Credential Manager or the Linux
  Secret Service. The session file on its own (in a backup, a synced folder,
  a stolen disk image without your login) is useless. Where no credential
  store exists, `hme login` warns and falls back to a plain file readable
  only by you (`0600` in a `0700` directory), and refuses to read it back if
  those permissions have been loosened.
- **It only talks to Apple.** Every request is checked to be HTTPS (TLS 1.2+)
  to an `apple.com`, `icloud.com` or `icloud.com.cn` host, including service
  URLs that arrive in Apple's own responses. Redirects are not followed.
  Cookies are scoped by domain as a browser would scope them.
- **Nothing sensitive in output.** `--debug` prints method, host, path and
  status only: no headers, bodies or query strings. Error messages leave out
  query strings (they carry account identifiers). Text that came from the
  network is stripped of control characters before it reaches your terminal.
- **Deleting is deliberate.** `delete` matches addresses exactly, shows what
  it will remove and asks first.
- **No telemetry**, and no network access other than the above.

What this does not protect against: malware running as your user while you
are logged in can ask the credential store for the key just as `hme` does. A
stored session grants access to iCloud web services for your account until it
expires or you revoke it; `hme logout` removes it locally, and *Sign Out of
All Browsers* in iCloud.com account settings ends it on Apple's side.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## How it works

Apple has no public API for Hide My Email, so `hme` speaks the private one
that icloud.com uses: SRP sign-in and two-factor verification against
`idmsa.apple.com`, a token exchange with `setup.icloud.com`, then the
`/v1/hme/*` and `/v2/hme/list` endpoints of the account's mail-domain service.
Because that API is unofficial, Apple can change it without notice.
[PLAN.md](PLAN.md) has the design and the reasoning behind it.

## Development

```bash
git clone git@github.com:dewab74/hide-my-email.git
cd hide-my-email
pre-commit install      # required once per clone; see below
make test               # go test -race ./...
make lint               # golangci-lint
make build
```

**Run `pre-commit install` after cloning.** Formatting, linting
(golangci-lint with gosec, markdownlint) and secret scanning (gitleaks and
detect-secrets) run on your machine before a commit is created, because a
secret that reaches the remote is already leaked. Git hooks live in `.git/`
and are not cloned, so without this step nothing is checked. CI runs the same
hooks only as a backstop for commits made with `--no-verify`.

Tests never contact Apple. `internal/icloud/icloudtest` is a fake that
verifies SRP proofs and cookies for real, and the CLI tests drive the whole
program against it.

Layout:

```text
cmd/hme/                  entry point
internal/cli/             commands, prompts, output
internal/icloud/          sign-in, session handling, Hide My Email API
internal/icloud/icloudtest/   fake Apple for tests
internal/srp/             SRP-6a as Apple uses it
internal/store/           encrypted session storage
Formula/hme.rb            Homebrew formula, generated on release
scripts/                  release helpers
```

### Releasing

```bash
git tag v0.2.0 && git push origin v0.2.0
```

Pushing a `v*` tag runs the release workflow: it runs the tests, has
GoReleaser build the six platform binaries and `checksums.txt`, publishes
them as a GitHub release with a provenance attestation, and then regenerates
`Formula/hme.rb` from those checksums (`scripts/update-formula.sh`) and
commits it to `main`, which is what `brew upgrade` picks up. Pre-release tags
such as `v0.2.0-rc1` are published but leave the formula alone.

## Acknowledgements

The protocol knowledge here stands on the work of
[lroolle/ihme-cli](https://github.com/lroolle/ihme-cli) and
[mark-liu/hme](https://github.com/mark-liu/hme) (both MIT), and through them
on pyicloud and rclone's iCloud backend.

`hme` is not affiliated with or endorsed by Apple. iCloud and Hide My Email
are trademarks of Apple Inc.

## License

[MIT](LICENSE)
