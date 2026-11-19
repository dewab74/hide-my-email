# Security policy

`hme` holds a session for your Apple Account, so security reports are taken
seriously and handled first.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private reporting instead:
**Security → Report a vulnerability** on this repository.

Include what you found, how to reproduce it, and the version (`hme version`)
and operating system. Never include a real session file, password, cookie or
verification code; if a trace is useful, `hme --debug` output is designed to
be safe to share, but read it before you post it.

You should get an acknowledgement within a week.

## Scope

In scope: anything that exposes the password or session, sends credentials
anywhere but Apple, weakens the encryption of the stored session, lets
remote data drive the terminal, or deletes addresses the user did not name.

Out of scope: attacks that require already running code as the logged-in
user, and behaviour of Apple's services themselves.

The design and threat model are described in [PLAN.md](PLAN.md#security-model).
