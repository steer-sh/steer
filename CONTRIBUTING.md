# Contributing to Steer

Bug reports, questions and pull requests are welcome.

## Before you start

Open an issue before a large change, so we can agree on the approach before you
write it. A small fix can go straight to a pull request.

Read [ARCHITECTURE.md](ARCHITECTURE.md) before you change a component boundary.
[CLAUDE.md](CLAUDE.md) lists the files that must change together and how to run
each kind of test; it is written for coding agents, and it works for people too.

## Making a change

Build the CLI with `cd cli && ./build.sh`, then run the checks from the
repository root. `check.sh` runs the `steer` it finds on PATH, or the one
`STEER_BIN` names; point it at this checkout's launcher to use your build:

```bash
STEER_BIN="$PWD/scripts/steer" ./check.sh
```

`check.sh` must pass before a pull request is merged. Besides formatting, build,
vet and the Go tests, it runs `scripts/revert-test.py`: each Go test you add must
fail under at least one variant of your change, such as one changed block put
back to its base version. The variants are listed in the script's docstring. A
test that pins behaviour that already existed says so with
`// pins existing behaviour` in its doc comment.

## Running your build

The marketplaces install the published `release` branch, never this checkout.
To try your build in Claude Code, build it, place the stable executable, and
load this directory as the plugin for one session:

```bash
cd cli && ./build.sh && cd ..
./scripts/steer bin install
claude --plugin-dir "$PWD"
```

Codex has no equivalent; it runs the marketplace copy.

## Commit messages

You do not need to follow this repository's commit message format. Pull requests
are squash-merged, and the maintainer writes the final commit message from your
description. Say in the pull request what changed, why, and anything you tried
that did not work: that is what the commit message keeps.

## License

By contributing, you agree that your contribution is licensed under the
[MIT License](LICENSE).
