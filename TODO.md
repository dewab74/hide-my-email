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
- [ ] Tag `v0.1.0` and check the release workflow end to end

## Next (0.3)

- [ ] Hardware security key (FIDO2) sign-in; today it is detected and
      reported as unsupported
- [ ] China-region accounts: follow the region hint in a 421 answer rather
      than relying on the account-country header alone
- [ ] Explain Advanced Data Protection (`pcsRequired`) accounts specifically
- [ ] Homebrew tap, Scoop bucket, winget manifest
- [ ] Sign and notarize the macOS binaries; sign the Windows ones
- [ ] `hme login` should offer a choice when several trusted phone numbers
      exist

## Later

- [ ] File lock so two concurrent `hme` runs cannot interleave session writes
- [ ] `hme list --since/--before`, and `--output csv`
- [ ] `hme delete -` and friends reading addresses from stdin
- [ ] Shell completion of addresses from a short-lived local cache
- [ ] Man page generated from the command tree
- [ ] `hme logout --everywhere` if Apple exposes a way to end the web session

## Known limitations

- The iCloud web API is private and can change without notice.
- Accounts must have "Access iCloud Data on the Web" enabled.
- `hme logout` removes the session locally but cannot revoke it at Apple.
