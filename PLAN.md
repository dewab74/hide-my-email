# Plan

What `hme` is for, the decisions behind it, and where it goes next. The
task-level checklist is in [TODO.md](TODO.md).

## Goal

A fast, single-binary, verb-style CLI for iCloud Hide My Email that is
pleasant to use by hand, dependable in scripts, and careful with the
credentials it has to hold.

```text
hme generate -d "Description" [--json]
hme list --active [--json | --table]
hme delete someone@icloud.com
```

### Requirements

| Requirement | How it is met |
| --- | --- |
| Verb-style CLI | Cobra commands: `generate`, `list`, `show`, `copy`, `update`, `deactivate`, `reactivate`, `delete`, `forward`, `login`, `logout`, `status`. |
| Copy generated address | `generate` copies by default; `--no-copy` opts out; a missing clipboard is a note, not a failure. |
| macOS, Linux, Windows | Pure Go, no cgo; cross-compiled for amd64 and arm64 on all three and tested on all three in CI. |
| Fast compiled binary | One static Go binary; starts in milliseconds; a command is normally one or two HTTPS requests. |
| Security focused | See [Security model](#security-model). |
| Repo tooling | pre-commit (format, lint, secret scan), golangci-lint, CI matrix, govulncheck, Dependabot, GoReleaser. |
| Secrets stay out of the repo | gitleaks and detect-secrets as commit hooks; `.gitignore` covers session files and env files; tests use fixtures against a local fake. |

### Non-goals

- Reading the mail sent to an address, or anything else in iCloud.
- A TUI, a daemon, or agent/MCP integration.
- Supporting accounts without iCloud+.

## What was learned from the reference projects

Two existing tools were reviewed before writing any code.

**[mark-liu/hme](https://github.com/mark-liu/hme)** (Go, ~1.5k lines) is
small and clear. It authenticates by decrypting iCloud cookies out of
Chrome's cookie store. Taken from it: the two-step create (`generate` then
`reserve`), "address on stdout, copy to clipboard" ergonomics, the response
size cap. Not taken: cookie extraction. It is macOS- and Chrome-specific,
needs Keychain access to Chrome's storage key, stores cookies in plain text,
supports only create and list, and hard-codes one API host.

**[lroolle/ihme-cli](https://github.com/lroolle/ihme-cli)** (Go, much
larger) signs in natively and has learned a great deal about Apple's
behaviour in the field. Taken from it: the SRP parameters and Apple's
variations on them, the sign-in sequence, the two-factor quirks (codes
accepted with HTTP 409, explicit push requests, SMS-only accounts), the full
set of alias endpoints, the loose shapes of Apple's error envelope, session
recovery by `accountLogin`, query-string redaction, and atomic session
writes. Not taken: the inbox reader, share server, agent, TUI and MCP
layers, which are outside this tool's goal; and its plain-text session file
and send-all-cookies-everywhere behaviour, which are replaced below.

One point the sources disagree on: for the legacy `s2k_fo` password
protocol, ihme-cli feeds PBKDF2 the upper-case hex digest, while pyicloud
and rclone use lower-case. `hme` uses lower-case. Current accounts negotiate
`s2k`, so this only matters for old ones; it is listed in TODO.md to verify.

## Decisions

### Go

Both references are Go, so the protocol logic ports directly and can be
compared line by line. Go gives a static binary, trivial cross-compilation
to all six targets without cgo, a strong standard library for exactly what
is needed (TLS, `crypto/pbkdf2`, AES-GCM, `math/big`), and few dependencies.
Rust would also satisfy "fast compiled binary" but with more code and more
third-party crates for the same result.

Direct dependencies are kept to five: `spf13/cobra` (CLI, and its `doc`
package with go-md2man for the manual pages), `golang.org/x/term` (hidden
password input), `atotto/clipboard`, and `zalando/go-keyring` (credential
store).

### Native sign-in rather than browser cookies

SRP sign-in works the same on every platform, needs no browser, and means
the tool never handles the password in a recoverable form. The cost is
implementing Apple's sign-in and tracking it when it changes.

### Session encrypted at rest, key in the OS credential store

The session (tokens and cookies) is as good as the password for iCloud web
access, so it should not sit in a plain file. Putting the whole session in
the credential store does not work: Windows Credential Manager caps a secret
at about 2.5 kB and a session is larger. So the file is encrypted with
AES-256-GCM and only the 32-byte key goes in the credential store. If there
is no store, fall back to an owner-only file and say so plainly.

### Requests can only go to Apple

Service URLs come out of Apple's responses, and cookies arrive with a
`Domain` attribute. Both are checked against an allow-list of Apple domains,
HTTPS is required, and redirects are not followed, so a tampered or
unexpected response cannot make the tool send credentials elsewhere. Cookies
are scoped per domain, as in a browser, rather than replayed to every host.

### Output contract

Results on stdout, everything conversational on stderr; `--json` everywhere
with a stable, tool-owned shape; distinct exit codes for usage errors and for
"sign in again". This is what makes the tool composable.

### Destructive actions

`delete` resolves addresses by exact match only (no fuzzy matching), lists
what it will delete, and asks, unless `--yes`. Without a terminal and without
`--yes` it refuses. `copy`, which is harmless, is the only command that
matches loosely.

## Architecture

```text
cmd/hme            main: wires stdin/stdout, clipboard, signals
  internal/cli     cobra commands, prompts, tables/JSON, exit codes
    internal/icloud    Client: Login, Resume/Refresh, List, Create, Update,
                       Deactivate, Reactivate, Delete, SetForwardTo
      internal/srp     SRP-6a client (and a server, for tests)
    internal/store     envelope-encrypted session file + keyring key
  internal/icloud/icloudtest   fake Apple used by icloud and cli tests
```

A command runs as: load and decrypt session → `Resume` (trust a session
validated in the last 10 minutes, otherwise ask Apple) → call the API,
recovering once through `accountLogin` if the service refuses the cookies →
save the session if anything in it changed.

## Security model

| Threat | Mitigation |
| --- | --- |
| Password disclosure | Never sent (SRP), never stored, read without echo; no password environment variable or flag; `--password-stdin` refuses a terminal. Buffers `hme` controls are wiped after use. That is best effort: Go strings and the garbage collector mean copies can outlive it, notably a password that arrived from `op` as JSON. |
| Session file read from backup, sync or another account | AES-256-GCM, key in the OS credential store. Without a credential store the fallback is a `0600` file in a `0700` directory; on Unix it is rejected if the file is readable by others or the directory is writable by others. Windows relies on the profile directory's ACL and is not checked. |
| Session file tampering or substitution | GCM authentication with bound associated data. A plaintext file is refused when this machine holds a session key, so encryption cannot be sidestepped by swapping in an unencrypted file, and a session loaded encrypted is never re-saved in the clear. Not covered: rolling back to an older encrypted copy of the same session. |
| Credentials sent to a non-Apple host | HTTPS-only allow-list on every request URL and cookie domain; no redirects. Cookies are scoped by domain; path and expiry are not tracked, so an expired cookie may be re-sent to the Apple host that issued it. |
| Hostile server parameters | SRP rejects `B ≡ 0 (mod N)` and `u = 0`; PBKDF2 iteration count and salt length bounded in both directions; response size capped. |
| Secrets in logs, errors or bug reports | Debug trace has no headers, bodies or query strings; endpoint names are percent-encoded and cleaned; a test asserts the trace contains no secret. `op`'s output is never printed. |
| Terminal escape injection | Control and format characters stripped from network text, endpoint names and `op` error text before printing. |
| Accidental deletion | Exact match, preview, confirmation. Deleting an active address deactivates it first; if the delete then fails, the error says it was left deactivated, and a batch reports what was already done. |
| Argument injection into `op` | Run with an argument list and no shell or stdin; values attached to flags with `=`; names and assignments after `--`. |
| Secrets committed to the repo | gitleaks and detect-secrets hooks at commit time, CI backstop, ignore rules. |
| Supply chain | Five direct dependencies, `go.sum`, Dependabot, govulncheck in CI, actions pinned by commit SHA and tools by version. The release runs as three jobs: tests with a read-only token, the build with release and attestation rights and no build cache, and a formula job that alone may push to `main`. A tag must be on `main` to release, and the Homebrew formula never moves to an older version. Pre-commit hooks are pinned by tag, not SHA. |

Reviewed on 2026-11-19 by two independent passes (security and
correctness) and scanned with grype, trivy, govulncheck, gosec, gitleaks,
zizmor and actionlint; the findings and what was done about each are in
git history under "review".

Out of scope: an attacker already running code as the logged-in user.

## Testing

No test contacts Apple or needs a credential. `icloudtest.Fake` implements
the identity, setup and Hide My Email services on a local server and checks
SRP proofs (via a real SRP server implementation) and cookies, so the client
has to carry out the protocol correctly to pass. On top of that, the CLI
tests run whole commands end to end: prompts, clipboard, files on disk, exit
codes. The remaining gap is by nature the live service; closing it is the
first milestone below.

## Milestones

1. **0.1, released:** everything above, tested against the fake;
   binaries, checksums and a Homebrew formula published by the release
   workflow.
2. **0.2, live verification:** sign in and run each verb against real
   accounts (device 2FA, SMS 2FA, a repeat login with a trust token) on all
   three operating systems; fix what differs. Done once on macOS with no
   defects found; Linux, Windows and the cases listed in TODO.md remain.
3. **0.3, reach:** hardware security key sign-in, China-region accounts,
   Scoop/winget packaging, signed and notarized macOS binaries.
4. **1Password, done in 0.1.2 and 0.1.3:** `hme login --1password` signs
   in from a vault item, and `hme generate --1password --url` creates the
   address and a login for the site in one step, with the password
   generated and held by 1Password via the `op` CLI so `hme` never handles
   it.
5. **Later:** a file lock for concurrent runs, `list` filters by date,
   bulk operations from stdin, address completion in shells.
