# Security policy

## Reporting a vulnerability

Report it privately through GitHub, using
[**Report a vulnerability**](https://github.com/infrena/infrena-provider-fake/security/advisories/new)
on this repository's Security tab. That opens a private advisory visible only to you and
the maintainers.

Please do not open a public issue for a security problem, and please do not disclose it
publicly until a fix is released.

If the problem is in the engine rather than this plugin, report it against
[infrena/infrena](https://github.com/infrena/infrena/security/advisories/new). If you are
unsure, report it in either and it will be routed.

## Supported versions

Only the most recent release. This plugin is pre-1.0 and tracks the engine closely.

## What is in scope

This provider manages nothing real. It has no credentials, contacts no network, and its
entire "cloud" is a JSON file on disk. That makes its attack surface small but not empty:

- **It is a downloaded binary that infrena executes.** Anything that lets a different
  binary run in its place, or that lets it do more than read and write its own cloud file,
  is a vulnerability.
- **It is the reference a plugin author copies.** A pattern here that is unsafe to imitate
  is worth reporting even though this plugin itself is harmless, because the next plugin
  written from it will not be.
- **Sensitive values.** The engine marks some attributes sensitive and expects a provider
  not to leak them. If this one does, the bug is worth fixing here and is probably worth
  checking for in the engine too.

## What is known, and not a vulnerability

- **The cloud file is plain, readable JSON, and is meant to be.** Editing it by hand is how
  drift is induced in tests. It is not a secret store, and nothing in it should be real.
- **This plugin is for testing and for learning from.** Pointing it at anything that
  matters is a misuse, not a vulnerability.
