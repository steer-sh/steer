# Security policy

## Supported versions

Only the latest release receives security fixes. The `release` branch and the
newest `v*` tag are the supported versions.

## Reporting a vulnerability

Do not open a public issue for a security problem.

Report it privately through GitHub:
https://github.com/steer-sh/steer/security/advisories/new

Include:
- the Steer release (its `v*` tag) and the harness (Claude Code or Codex)
- the steps or the input that shows the problem
- what an attacker gains

## What to expect

Steer has one maintainer. Reports receive a reply when the maintainer reads
them, and a fix ships in a new release. The advisory credits the reporter
unless they ask otherwise.

## Scope

In scope: the `steer` executable, the bundled skills and plugin files, and
the hooks and guards that Steer generates and registers. Examples are a
repository file that makes Steer write outside its stores, run code during a
review, or change harness configuration.

Out of scope: vulnerabilities in Claude Code, Codex, or the reviewer CLIs
themselves. Report those to their vendors.
