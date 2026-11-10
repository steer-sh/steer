# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-11-10

### Added

- `reviewer.rounds` sets how many review rounds the commit gate can block, from 1 to
  10. The default stays 4, and the final review after the limit still runs. Each
  review row records the limit in force as `max_firings`.
- At session start and subagent start, an agent whose commit gate is armed receives
  the commit form the gate accepts, so it no longer learns the form from a refusal.
  Codex also receives the absolute `git -C` repository path.
- Review rows record each reviewer run's outcome and elapsed time as `reviewer_runs`,
  and the round's as `review_elapsed_ms`.
- `commit-gate mine findings` and `steer fix match` take `--kind repaired` and
  `--kind deferred`, which split accepted findings by the `ACCEPTED, not fixed`
  ruling. Text listings label a deferred finding `[ACCEPTED, not fixed]`.

### Changed

- Steer ships no presets. A guard that an earlier Steer installed from a preset keeps
  running: `steer preset list` names them, `steer preset disable <name>` removes one
  and `steer preset sync --prune-orphans` removes all of them, archiving each. Each
  command reads one store, the project's by default; add `--scope global` for the
  global store. `steer preset enable`, the bundles and the dashboard's built-in guard toggle are
  removed, and the status JSON drops `presets[]`.
- Self-protection is part of Steer and on by default, where it was installed only
  beside a preset. One global guard runs in each harness where Steer is set up, and
  `steer status` reads it as `posture.protection`. Only you turn it off:
  `steer config set protection.self off`.
- Self-protection leaves harness configuration files (`settings.json`, `hooks.json`,
  `config.toml`) open to Edit and Write. It blocks only a result that drops, moves or
  disables a hook Steer registered. Steer's own files stay closed, and the block
  names the steer command to run instead.
- A review round whose findings are all rated NITPICK no longer blocks the commit.
  The agent sees the findings, the commit proceeds and the review sequence ends.
- The reviewer prompt rates severity by who meets the failure and what it costs, and
  a finding that fits two levels takes the higher. Review rows after this release
  carry a new instrument ID.
- The undecided findings dump lists the findings no `/steer-report` pass read first,
  under `=== new since the last read`, and `/steer-report` reads that section in
  place of the whole pile.
- `steer bin install` and the session-start line say that stale guards need a
  rebuild, and leave a project guard runner's rebuild and commit to you.

### Fixed

- The commit gate refuses a forced add before a commit when it cannot read the add's
  paths. A bundled `git -C sub add -f x && git commit` sent every ignored file in
  the repository, a `.env` among them, to the reviewers.
- The refusal of an unquoted git option value, such as `git -C $D init`, names the
  word and teaches the quoted form.
- `steer bin install` no longer overwrites a file named `steer-self-protection.py`
  that no manifest entry owns. It names what holds the file and the command that
  frees it.

## [0.2.0] - 2026-11-07

### Added

- `steer uninstall` prints every global Steer registration for each agent (commit
  gate, check runner, guard runner, rules section, status line) and removes them
  with `--apply`. A wrapped status line is given back. `--purge` also deletes
  `~/.steer`. Project registrations are committed files, so it names them and
  changes none. Run it before you remove the plugin.
- After a plugin update, the next session start installs the new build to
  `~/.steer/bin/steer`, repoints the hooks and rebuilds the global guard runner, and
  prints one line such as `steer: hooks updated 0.1.7 → 0.2.0`. It acts only where a
  global Steer registration already calls `~/.steer/bin/steer` or runs the global
  guard runner.
- The status line and the commit gate's operator notice name a plugin that you
  removed while its hooks still run, with the uninstall command.
- The audit queue gives each fix a turn, so one busy fix no longer holds the others
  behind its backlog. A fix with three clean readings moves to the reserve. A case
  that an agent contested, stopped on again, hit more than once in a session, or
  whose check detector moved is flagged and read first.
- `/steer-report` reads audit cases in a reader subagent on the same model, up to 12
  cases at 8,000 bytes each, and returns only what needs a decision. A harness with
  no subagent reads 3 cases inline.
- The commit gate tells the CLI reviewers which paths have unstaged edits on a
  staged-only commit, because the copy on disk is not what lands.

### Changed

- A guard command that names no harness (`register`, `remove`, `preset enable`,
  `rules add`, `archive restore`, `hooks sync`, `hooks rebuild`) and the dashboard's
  guard actions write only to harnesses where Steer is set up. Use `--harness` or
  `--all-harnesses` to add a harness. A fresh machine's first guard still reaches
  every installed harness.
- Codex: the project guard runner command finds the repository root, so a session
  started from a subdirectory runs the guards instead of blocking every call. Run
  `steer hooks sync --scope project`, then approve the changed command in `/hooks`.
- Project harness files (`.claude/settings.json`, `.codex/hooks.json`, `.pi/`, the
  project `CLAUDE.md` and `AGENTS.md`) resolve at the repository root from any
  subdirectory, so a sync from a subdirectory no longer writes a second set.
- The commit gate's caller context searches every changed word, weighted by rarity,
  in every language, and says when its search is incomplete.
- `steer status` without `--json` skips the transcript scan. The queue's transcript
  candidate comes from this machine's stored snapshots, and only while a complete
  `steer report refresh` covers the window. The status JSON shape changed.
- A check whose detector you edited stays tracked by its record. Only an id that now
  names another check reads as changed.
- Every skill command runs the launcher's absolute path, so a "don't ask again"
  permission answer applies to later commands.
- The retired Python gate's replay drivers, its Stop-hook recognition and the
  `COMMIT_REVIEW_STATE` fallback are removed. Use `COMMIT_GATE_STATE_DIR`.

### Fixed

- The `python-command-injection` preset and generated Python guards judge a command
  passed as `args=` or `command=`. Run `steer preset sync --scope <scope>` to update
  an installed preset.
- The commit gate refuses an add before a commit that uses a shell glob or brace
  expansion, which it read as a pathspec and so reviewed only part of the commit.
- `steer hooks sync` refuses, and `steer status` warns, when the guard runner stamps
  an old rule on a guard whose source is current. Run
  `steer hooks rebuild --scope <scope>`.
- A guard firing inside a Claude Code subagent is read from the subagent's own
  transcript.
- The review ledger keeps every row once while a rewrite stalls.
- A check.sh block with no session ID no longer takes another session's next Stop.
- `steer bin install` rebuilds a stale global guard runner only in the harnesses
  that register it.
- The possible-commit refusal of an interpreter heredoc names the exit: a file tool
  or a quoted document heredoc.

## [0.1.7] - 2026-11-05

### Changed

- `steer report` marks an instruction bullet cut at 200 bytes with a trailing `…`,
  and `digest_version` is now 8. Run `steer report` again: a stored report is refused.
- Repository checks: the Go detector skips a subdirectory that holds its own `go.mod`
  and refuses a case whose replay embeds files the repository does not. Each Go
  detector needs one `steer check test`, and a check-tier fix reads as an edited
  check until it is registered again.

### Fixed

- The commit gate's preflight accepts `cd /abs/path; git commit`,
  `cd /abs/path || exit; git commit` and `set -e` scripts when the `cd` is plain and
  its target is a directory it can enter.
- The commit gate reads `git diff` with the repository's display settings pinned, so
  `diff.suppressBlankEmpty`, `color.ui=always`, `diff.noprefix` and `diff.external`
  no longer change or drop the diff that reviewers and the fixture miner read.
- The pre-commit install and `steer git-hooks install` refuse to run from inside
  `.git`, where a relative `core.hooksPath` resolved to a directory git never runs.
  A hooks path such as `link/../hooks` resolves through the symlink, as git does.
- `steer verify --staged` reads a staged file whose name starts with a digit and a
  colon, and both modes read the right paths under `diff.relative=true`. Verify warns
  when a file is not UTF-8, because the guard reads a substituted copy.
- The review ledger no longer loses or duplicates a row that is appended while a
  rewrite holds the shard, and two chains with identical blocks in one reply count
  twice, not once.
- `steer report` reads a multi-line guard rule as guard-backed, as sync writes it.
- Repository checks: a Go test that did not finish reads as failed only when it ran
  alone; a failed iteration under `-count` stays failed.
- Codex: a check.sh Stop block after a turn ends no longer anchors to that turn.
- Reviewer settings: a flag named only in a shell comment no longer reads as pinned,
  and the comment's text is not rewritten.
- The TUI reviewer editor records a keep or drop answer only while its question is
  the note on screen.

## [0.1.6] - 2026-11-04

### Added

- `steer report` lists `correction_candidates`: typed messages that a fixed word list
  flags and no correction pattern matched. Each candidate is unconfirmed, and
  `/steer-report` reads each one against its context before it uses it. The index
  stamps a digest of the list as `correction_list`, and `digest_version` is now 7.

### Fixed

- A guard blocks even when it writes to the process's standard output directly, for
  example through a subprocess. That output reached the harness before the deny, so
  the call went through. Output that bypasses `sys.stdout` now goes to stderr, so a
  guard that replied only through file descriptor 1 stops blocking. Run
  `steer hooks rebuild --scope <scope>` to update an installed master.
- Presets skip a malformed edit shape instead of raising, which allowed the edit with
  no audit row. The postinstall guard matches a key written with `\u` escapes, and the
  generation prompt's postinstall snippet gets the same treatment.
- Presets and the generation prompt: the injection reference visitor treats
  `subprocess.call` with `shell=True` as a shell sink, as the preset does.
- A guard file that no manifest entry owns no longer carries another entry's rule and
  identity. Its block footer offered `steer remove` for the wrong file. `steer list`
  shows such a file as `<name>.py (unowned)`.
- Repository checks: a Go detector's digest ignores OS metadata such as `.DS_Store`
  beside an embedded file, so a receipt reads the same on every machine.

## [0.1.5] - 2026-11-01

### Added

- `steer status` leads with one verdict and the version: `healthy`, `needs attention`
  or `could not check everything`. An issue with a known fix names it on one copyable
  line, and `posture.health` in the JSON carries the same verdict.
- `steer status` reads Codex hook approval from the saved hash and the enabled state,
  so an approved hook no longer warns and a hook that is turned off shows as a setup
  step. The check runner is covered too.
- The machine name is pinned in `~/.steer/host`, so a hostname change no longer makes
  this machine's fixes read as another machine's. `steer host --former` declares a
  name the machine used before.

### Changed

- `steer commit-gate remove` needs `--harness` and `--scope`, and a refusal lists the
  registered gates and resolvers. It reports whether it removed anything.
- Repository checks: replay and the detector digest changed, so each Go detector needs
  one `steer check test`. A check-tier fix reads as an edited check until it is
  registered again.
- `steer remove` refuses an entry that names the manifest, the master or a file that is
  not `.py`, and it refuses a guard file with no id of its own.

### Fixed

- A Claude Code project guard runner works from any directory. A `cd` into a
  subdirectory failed every later tool call. `steer hooks sync` or `steer bin install`
  rewrites the registration.
- `steer remove` and `steer test` take an exact id first. On a store where an entry
  names another file, the confirmation and `steer test` could read the wrong guard.
- `steer list` shows the id and rule of the entry that owns each file.
- Guard file ownership follows the entry key, so a second id cannot overwrite a guard.
  A preset whose id changed now converges on sync.
- `steer verify --staged` and `--changed` read paths with non-ASCII characters. The
  pre-commit hook refused such a commit even with no guards.
- A multi-line manifest rule syncs as one bullet. It could grow the section on each
  sync or stop later syncs.
- Presets and the generation prompt: the injection reference visitor forgets a
  shell-safe proof when its name is rebound, including `def`, `class` and match names.
- Security: a project store refuses a linked `.gitignore`, `.steer/checks` or check
  manifest, and fixture writes refuse a linked `.steer`. A fixture's hook must be a
  guard name, and the audit reader treats a linked sidecar as damaged.
- `steer check install`, `steer statusline install` and `steer commit-gate install` no
  longer leave `.steer/hooks/.gitignore` in a repository with no guards.
- Commit gate: a denied commit from a subdirectory no longer reports every committed
  project addendum entry as removed.
- Repository checks: Go detector replay keeps `//go:embed` lines, handles a local named
  like an import, and refuses a name that means two packages. Replay keeps the
  detector's mode and compares it again immediately before the check.
- `steer status` prints the hooks install first, because a rebuild before it embedded
  the old runtime. The fix for a check runner that never ran is now a real command.
- Dashboard: the reviewer editor accepts a name that starts with `d`, and an arrow key
  no longer answers the carry question before it is asked or clears the model.
- Stored diffs keep hunk content across a blank context line, reviewer flag removal
  reads shell escapes, a long instruction bullet is cut on a character boundary, and
  `steer audit` counts a repeated Codex Stop block with no turn id.

## [0.1.4] - 2026-10-30

### Added

- Commit gate: an `openai-compatible` reviewer provider reviews through an API key,
  with no reviewer CLI installed. It works with an OpenAI-compatible endpoint, such
  as OpenRouter, over https (plain http only on a loopback host). Set
  `reviewer.api.base_url` and a model; `/steer-setup` covers the path.
- `steer reviewer key set` stores the API key in
  `~/.steer/credentials/reviewer-api-key`. It reads from a terminal with no echo and
  refuses a pipe. The key can also come from the variable that
  `reviewer.api.key_env` names, and it never goes in `config.json`.
- `reviewer.api.tools` (`off`, `auto`, `required`; default `off`) lets an API
  reviewer call `read_file`, `grep` and `list_files` over each file as the commit
  lands it. Tools send more code to the endpoint and multiply the input tokens.

### Changed

- The commit-review ledger now has one lock for appends and rewrites, and the
  resolver reads under it. A binary older than 0.1.4 honours neither lock, so run
  `steer bin install` after the plugin update to keep the plugin copy and the stable
  executable on one version.
- The Go toolchain moves to 1.27.3 for GO-2026-6809 in `crypto/x509`.

### Fixed

- `steer git-hooks install` writes where git runs hooks. It reported success for a
  hook git never ran under `core.hooksPath`, and it failed in a linked worktree.
- Fixture runs and `steer test` read a guard's decision from stdout alone. A warning
  on stderr could turn a deny into an allow, and stderr that quoted a block could
  read as one.
- Presets: the postinstall guard checks the raw text of content that decodes to any
  JSON shape. Content that decoded to a non-object allowed the edit silently.
- Presets: the injection guard forgets a shell-safe proof when its name is rebound.
- Presets: self-protection reads every `steer` word in a command past its flags and
  shell punctuation, so those forms no longer hide the removal of a factory hook.
- Repository checks: a Go test unfinished when its test binary exits reads as failed
  and keeps the crash output. It read as unavailable before.

## [0.1.3] - 2026-10-26

### Changed

- A check.sh Stop block now names the other sessions that ran checks in the same
  working tree in the last hour. It tells the agent not to edit their work, and to
  record a contest when the failure does not come from its own task.
- The README's Update section gives the Claude Code commands that start a plugin
  update.

### Fixed

- `steer status` no longer offers the `bin install` of an earlier Claude Code
  plugin copy that the plugin cache keeps after an update.
- Commit gate: a Claude Code subagent keeps its own commit chain, so its first
  commit no longer reads as a retry of its parent's denied commit.
- Commit gate: one large caller file no longer keeps every later caller out of the
  review context.
- Commit gate: a ledger round that names no verdict, such as "see above", no longer
  counts as a round with zero concerns.
- Field counts no longer drop a finding when two chains ledger the same concern
  text in one reply.
- `steer status` and the dashboard read project registrations at the repository
  root when run from a subdirectory.
- `steer team lock`, `apply` and `verify` keep `steer-team.lock.json` at the
  project root, not in the working directory.

## [0.1.2] - 2026-10-25

### Changed

- Commit gate: before the author repairs a review finding, it must name the
  requirement that makes the repair necessary: the user's request, or code, docs or
  tests that existed before the change. Reviewers often construct edge cases that
  the repository never has to support. The author repaired them anyway, which
  added rounds and code without fixing a real failure. A finding with no such
  requirement can stay accepted but unfixed when the code stops safely on that
  input.
- Commit gate: a commit whose `cd` may not have run, or may have failed, is refused
  until it is rerun as `cd /path && git commit` or with `git -C`.

### Fixed

- Commit gate: a commit of a file with a non-ASCII name was reviewed without that
  file, or skipped as empty.
- Commit gate: a backslash escape in a path, a message or the `git commit` words
  could hide the commit or its repository from review.
- Commit gate: a commit whose `git` subcommand comes from a variable or an escape
  is refused as a possible commit.
- Commit gate: an exclusion pathspec in one `git add` no longer hides files that a
  later `git add` stages.
- Syncing the `## Steer Rules` section no longer deletes the headings and text after
  it.
- `steer verify` no longer writes synthetic firings into the live audit log.

## [0.1.1] - 2026-10-19

### Added

- `steer --version` prints the version, commit and build date of the running build.

### Changed

- `steer bin install` is the upgrade step after a plugin update: it also rebuilds a
  global guard runner built by an older Steer, and prints the rebuild command for a
  stale project runner, which the repository commits.
- The session-start message after a plugin update names the updated plugin's
  `bin install` command, and names a rebuild for every stale scope.

### Fixed

- Codex: a hook trust record counts under either spelling of a linked path.
- `/steer-setup`: a reviewer test that fails in Codex's network-off sandbox says so.

## [0.1.0] - 2026-10-17

First public release.

[0.3.0]: https://github.com/steer-sh/steer/releases/tag/v0.3.0
[0.2.0]: https://github.com/steer-sh/steer/releases/tag/v0.2.0
[0.1.7]: https://github.com/steer-sh/steer/releases/tag/v0.1.7
[0.1.6]: https://github.com/steer-sh/steer/releases/tag/v0.1.6
[0.1.5]: https://github.com/steer-sh/steer/releases/tag/v0.1.5
[0.1.4]: https://github.com/steer-sh/steer/releases/tag/v0.1.4
[0.1.3]: https://github.com/steer-sh/steer/releases/tag/v0.1.3
[0.1.2]: https://github.com/steer-sh/steer/releases/tag/v0.1.2
[0.1.1]: https://github.com/steer-sh/steer/releases/tag/v0.1.1
[0.1.0]: https://github.com/steer-sh/steer/releases/tag/v0.1.0
