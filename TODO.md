# TODO

Milestones and reasoning are in [PLAN.md](PLAN.md).

## Live verification (0.2)

Nothing here can be automated: each item needs a real Apple Account.

Verified on 2026-11-18 with hme 0.1.0 on macOS (arm64), against a real
iCloud+ account, using one throwaway address that was deleted afterwards:

- [x] `hme login` against live Apple (SRP sign-in and second factor)
- [x] `status`, `status --offline`
- [x] `list` with `--active`, `--inactive`, `--search`, `--sort`, `--quiet`,
      `--json` and the table
- [x] `generate` (two-step create, label and note, clipboard copy)
- [x] `show`, `copy`
- [x] `update` of the label and the note, and clearing the note with `-n ""`
- [x] `deactivate`, `reactivate`
- [x] `delete` of an active address (deactivates first), its refusal
      without `--yes` when there is no terminal, and unknown addresses
- [x] `forward` without an argument
- [x] Session stored encrypted with the key in the macOS Keychain; file
      `0600`, directory `0700`
- [x] `--debug` trace on a live request shows no query string or identifier
- [x] Homebrew install, formula test, and the release workflow end to end

Still to verify:

- [ ] `hme forward EMAIL` (changing the inbox). Not run on purpose: it
      changes forwarding for every address on the account
- [ ] `hme login` on an SMS-only account, and which second-factor path the
      login above took. The choice now follows Apple's `noTrustedDevices`
      statement when present; whether real accounts send it is unconfirmed
- [x] Second `hme login` reuses the trust token and skips the code
- [ ] `generate` with no note (an empty `note` on `reserve`)
- [ ] Confirm which status codes the setup and mail-domain services return
      for expired cookies (401, 421 and 450 are all treated as "refresh")
- [ ] Leave a session idle for a few weeks and confirm it recovers or asks
      for `hme login` cleanly
- [ ] Verify the `s2k_fo` password protocol on a legacy account (the
      reference implementations disagree on hex case; see PLAN.md)
- [ ] Session storage on Windows Credential Manager, GNOME Keyring/KWallet,
      and the no-keyring fallback
- [ ] Clipboard on Wayland, X11, Windows Terminal and over SSH
- [ ] Everything above on Linux and Windows

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

## 1Password integration

Done:

- [x] `hme generate -d DESC --1password --url URL [--username NAME]
      [--vault VAULT]` creates the address and a 1Password login for the
      site. Chosen over a separate `signup` verb: it is the same action
      with one more destination
- [x] The item is created through the `op` CLI with
      `--generate-password`, so 1Password generates and keeps the
      password; `hme` never prints `op`'s output and wipes its copy
- [x] Username defaults to the new address; `--username` overrides it and
      the address is then stored as the item's email field
- [x] `op` is run with an argument list, never a shell; assignments follow
      `--`; the URL must be http(s)
- [x] `op` and the vault are checked before the address is created; if
      saving the login fails afterwards the address is kept and the exact
      `op` command to finish is printed
- [x] Tests with a fake `op`; the real `op` 2.42.0 accepted the same
      arguments in a `--dry-run`

Still to do:

- [x] Run `hme generate --1password` for real (2026-11-19, op 2.42.0,
      macOS): address created, login saved with the address as username,
      and an unknown vault refused before any address was made
- [ ] Still unchecked against the real `op`: the item's `email` field when
      `--username` is given, and `hme login --1password` since its
      arguments changed to the `--flag=value` form (0.1.3)
- [x] The release workflow also updates `Formula/hme.rb` in the
      dewab-org/homebrew-custom tap, with a write deploy key scoped to
      that repository and to a tag-only environment
- [x] First release through the tap job (v0.1.4): formula pushed to the
      tap by the workflow and installed from it with `brew upgrade`
- [ ] `--tags`, and a password recipe for sites with awkward rules
- [ ] Decided against for now: writing the item reference into the
      address's note. The label and the item title already match, and it
      would cost a second iCloud call that can fail on its own
- [x] Sign in to iCloud through 1Password: `hme login --1password ITEM
      [--vault VAULT]`, with `op` supplying the Apple ID and password so
      nothing is typed
- [x] Run `hme login --1password` against the real `op` CLI and Apple
      (2026-11-19, hme 0.1.2, op 2.42.0, macOS)
- [ ] Open questions for 1Password sign-in:
  - whether to remember the item reference (not a secret), or read it
    from an environment variable, so a plain `hme login` uses it
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
- [x] Shell completion of addresses, inboxes and flag values
- [ ] Cache the address list briefly so repeated Tab presses do not each
      ask iCloud
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

## From the 2026-11-19 review, not fixed

Accepted for now, with the reason:

- [ ] SRP hashes `S`, `A` and `B` padded to the group size, following
      ihme-cli. Clients derived from pysrp hash them unpadded, which
      differs only when a value has a leading zero byte (about 1 login in
      100). If a correct password is ever rejected and a retry works, this
      is the first suspect. Needs a test vector from a real exchange
- [ ] Cookies are stored without path or expiry. An expired one may be
      re-sent to the Apple host that set it; dropping them locally risks
      needless re-authentication, so they are left to Apple to refuse
- [ ] An attacker who can write the session file can roll it back to an
      older encrypted copy (the associated data is constant)
- [ ] Windows: the session file's ACL is not checked
- [ ] Two `hme` processes at once (a command and a Tab completion) are
      last-writer-wins on the session file; it self-heals on the next
      call. Needs the file lock listed under Later
- [ ] Pre-commit hook revisions are tags, not commit SHAs
- [ ] `main` is not a protected branch, and there is no tag protection
      rule; both are repository settings

## Known limitations

- The iCloud web API is private and can change without notice.
- Accounts must have "Access iCloud Data on the Web" enabled.
- `hme logout` removes the session locally but cannot revoke it at Apple.
