# TODO

Milestones and reasoning are in [PLAN.md](PLAN.md).

## Before the first release (0.2)

Nothing here can be automated: each item needs a real Apple Account.

- [ ] `hme login` against live Apple with trusted-device 2FA
- [ ] `hme login` on an SMS-only account
- [ ] Second `hme login` reuses the trust token and skips the code
- [ ] `generate`, `list`, `show`, `copy`, `update`, `deactivate`,
      `reactivate`, `delete`, `forward` against the live service
- [ ] Confirm Apple accepts an empty `note` on `reserve` and `updateMetaData`
- [ ] Confirm which status codes the setup and mail-domain services return
      for expired cookies (401, 421 and 450 are all treated as "refresh")
- [ ] Leave a session idle for a few weeks and confirm it recovers or asks
      for `hme login` cleanly
- [ ] Verify the `s2k_fo` password protocol on a legacy account (the
      reference implementations disagree on hex case; see PLAN.md)
- [ ] Session storage on each OS: macOS Keychain, Windows Credential
      Manager, GNOME Keyring/KWallet, and the no-keyring fallback
- [ ] Clipboard on Wayland, X11, Windows Terminal and over SSH
- [x] Tag `v0.1.0` and check the release workflow end to end

## Next (0.3)

- [ ] Hardware security key (FIDO2) sign-in; today it is detected and
      reported as unsupported
- [ ] China-region accounts: follow the region hint in a 421 answer rather
      than relying on the account-country header alone
- [ ] Explain Advanced Data Protection (`pcsRequired`) accounts specifically
- [x] Homebrew formula for macOS and Linux, served from this repository
- [ ] A dedicated `homebrew-tap` repository, so installing is the single
      command `brew install dewab74/tap/hme`
- [ ] Scoop bucket and winget manifest for Windows
- [ ] Sign and notarize the macOS binaries; sign the Windows ones
- [ ] `hme login` should offer a choice when several trusted phone numbers
      exist

## 1Password integration (0.4)

Goal: one command that creates a Hide My Email address for a site and a
1Password login for it, with a generated password.

```text
hme generate -d "Acme store" --1password --url https://acme.example [--username NAME] [--vault VAULT]
```

- [ ] Decide the command shape: a `--1password` flag on `generate` (above)
      or a separate verb such as `hme signup URL -d DESC`
- [ ] Create the item through the `op` CLI (`op item create --category
      login --title DESC --url URL --generate-password username=...`), so
      1Password generates and stores the password and `hme` never sees it
- [ ] Username defaults to the new address; `--username` overrides it, and
      the address is then stored as the item's email field
- [ ] Record the address in the item and the item reference in the alias
      note, so each can be found from the other
- [ ] `--vault`, and optionally `--tags` and a password recipe
- [ ] Run `op` with an argument list, never through a shell; validate the
      URL (http/https only); keep the password out of stdout, stderr,
      `--json` and the clipboard unless explicitly asked for
- [ ] Decide what happens if `op` fails after the address exists: keep the
      address and say so, or deactivate it (leaning: keep, and print the
      exact `op` command to retry)
- [ ] Clear errors when `op` is missing, not signed in, or the vault is
      unknown; check for it before creating the address
- [ ] Tests with a fake `op` on `PATH`
- [ ] Possibly: sign in to iCloud through 1Password automatically, e.g.
      `hme login --1password [ITEM]`, with `op` supplying the Apple ID and
      password so nothing is typed. Open questions:
  - whether to remember the item reference (not a secret) so an expired
    session can re-login without flags
  - the two-factor code still comes from an Apple device or SMS and has
    to be typed; 1Password cannot supply it (Apple's code is not a TOTP),
    though the stored trust token usually avoids the prompt
  - never re-login silently in the background: a failed attempt counts
    toward Apple's lockout, so it stays an explicit command
- [ ] Later: other managers (Bitwarden) behind the same interface

## Later

- [ ] File lock so two concurrent `hme` runs cannot interleave session writes
- [ ] `hme list --since/--before`, and `--output csv`
- [ ] `hme delete -` and friends reading addresses from stdin
- [ ] Shell completion of addresses from a short-lived local cache
- [x] Man pages generated from the command tree
- [ ] `hme logout --everywhere` if Apple exposes a way to end the web session

## Ideas

Not planned yet; written down so they are not lost.

- [ ] macOS menu bar item to generate and manage addresses: one click (or a
      global shortcut) for a new address on the clipboard, plus a recent
      list with copy, deactivate and delete. Open questions:
  - a thin native app (Swift) that calls `hme --json`, so the CLI stays
    the single implementation, versus a separate client
  - it would be a long-running GUI process, which PLAN.md currently lists
    as a non-goal; it should live in its own directory or repository so
    the CLI stays a small static binary
  - how to prompt for sign-in and the two-factor code without a terminal
  - signing and notarization, which a menu bar app needs to be usable
- [ ] Safari extension to generate an address while filling in a sign-up
      form. Open questions:
  - Safari already offers Hide My Email in email fields for iCloud+
    users, so the extension has to add something Safari lacks: setting
    the label and note from the page, creating the 1Password login in
    the same step, or managing existing addresses
  - whether Chrome and Firefox, which have no built-in Hide My Email,
    are the more useful target; a WebExtension could cover all three
  - how the extension reaches `hme`: native messaging to the CLI keeps
    the session out of the browser, which is the safer design
  - Safari extensions ship inside a signed macOS app, so this likely
    shares a container app with the menu bar item

## Known limitations

- The iCloud web API is private and can change without notice.
- Accounts must have "Access iCloud Data on the Web" enabled.
- `hme logout` removes the session locally but cannot revoke it at Apple.
