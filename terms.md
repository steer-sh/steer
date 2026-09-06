# Steer terms

This glossary covers domain terms Steer presents in the README, CLI help and
CLI output descriptions, and the agent-facing `SKILL.md` and `CLAUDE.md` files.
`codex-hooks.md` is excluded because it is temporary. Ordinary English words
and implementation-only identifiers are excluded.

The two explanation columns use different audiences:

- **User documentation** means the README and CLI-facing help or output.
- **Agent documentation** means `skills/*/SKILL.md` and `CLAUDE.md`, which guide
  the agent rather than serving as user-level documentation.

`none` means the repository uses the term in that audience's material without
explaining what it means.

| Term | User documentation | Agent documentation |
|---|---|---|
| **adjudication** | The agent's decision about a reviewer finding. [README.md:5](/Volumes/MiniEx/projects/steer-skill/README.md:5) | The agent checks findings against the code, accepts or rejects them, and records the decisions. [skills/steer-report/SKILL.md:335](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:335) |
| **audit / audit firing** | none | The audit log records whether a guard fired during an intervention's after-window. [skills/steer-report/SKILL.md:378](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:378) |
| **backfill** | Reconcile unresolved ledger rows with local transcripts. [cli/cmd/commit_gate.go:584](/Volumes/MiniEx/projects/steer-skill/cli/cmd/commit_gate.go:584) | It joins ledger rows that owe adjudication evidence to harness transcripts. [CLAUDE.md:commit-gate resolver section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **baseline** | Steer tracks interventions against their baselines. [README.md:79](/Volumes/MiniEx/projects/steer-skill/README.md:79) | The baseline is the pre-install incidence of a class over eligible evidence units. [skills/steer-report/SKILL.md:410](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:410) |
| **cadence** | The SessionStart entry point refreshes the queue in the background and nudges the user when work is pending. [cli/cmd/report.go:216](/Volumes/MiniEx/projects/steer-skill/cli/cmd/report.go:216) | The session-start entry point refreshes the deterministic queue and nudges the user when work is pending. [skills/steer-report/SKILL.md:1](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:1) |
| **candidate** | none | A recurring evidence cluster that may deserve a check, guard, instruction, or prompt change. [skills/steer-report/SKILL.md:278](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:278) |
| **channel** | A place where the commit gate runs, such as a tool hook or Git pre-commit hook. [cli/cmd/commit_gate.go:66](/Volumes/MiniEx/projects/steer-skill/cli/cmd/commit_gate.go:66) | none |
| **chain** | none | A linked sequence of review rounds and findings; review-finding incidence counts chains. [skills/steer-report/SKILL.md:376](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:376) |
| **check** | The repository's verification command, such as `check.sh`. [README.md:129](/Volumes/MiniEx/projects/steer-skill/README.md:129) | A repository-level executable verification used when a class can be caught mechanically. [skills/steer-report/SKILL.md:308](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:308) |
| **commit gate / gate** | A panel of independent reviewers that examines a commit diff before it lands and can block while a finding stands. [cli/cmd/commit_gate.go:62](/Volumes/MiniEx/projects/steer-skill/cli/cmd/commit_gate.go:62) | none |
| **composition** | none | A softer measurement warning: the before and after populations overlap but their category mix shifted, or reviewer effort/run count changed. [CLAUDE.md:Drift is graded by dimension section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **confound** | none | A difference between measurement windows that prevents the effect being attributed to the intervention. [skills/steer-report/SKILL.md:412](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:412) |
| **corpus** | none | A body of evidence over which a class is counted: review findings, transcript friction, or auto-denials. [skills/steer-report/SKILL.md:278](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:278) |
| **correction** | A recurring correction is one kind of transcript evidence. [cli/cmd/report.go:33](/Volumes/MiniEx/projects/steer-skill/cli/cmd/report.go:33) | A user turn that corrects the agent and can contribute to a candidate cluster. [skills/steer-report/SKILL.md:290](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:290) |
| **denial / denials** | A tool call that Claude Code's auto-mode classifier blocked; Steer can record it. [cli/cmd/denials.go:23](/Volumes/MiniEx/projects/steer-skill/cli/cmd/denials.go:23) | none |
| **drift** | A comparison can become inconclusive when agents, reviewers, or work change. [README.md:79](/Volumes/MiniEx/projects/steer-skill/README.md:79) | A change in installed state or measurement conditions between expectation and observation. [CLAUDE.md:Drift is graded by dimension section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **effect** | none | After incidence divided by before incidence, reported as a confidence range whose bounds determine the verdict. [skills/steer-report/SKILL.md:86](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:86) |
| **evidence** | Review history and agent transcripts provide evidence for better checks, rules, and instructions. [README.md:5](/Volumes/MiniEx/projects/steer-skill/README.md:5) | Evidence includes corrections, tool failures, hook blocks, and denials used to identify recurring problems. [skills/steer-report/SKILL.md:278](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:278) |
| **finding** | A reviewer concern about the proposed change that the agent accepts or rejects. [README.md:5](/Volumes/MiniEx/projects/steer-skill/README.md:5) | none |
| **friction** | none | Recurring agent trouble in transcripts, including corrections, rejected calls, failures, and hook blocks. [skills/steer-report/SKILL.md:290](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:290) |
| **guard** | A Python check that examines tool calls before they run and tells the agent how to correct a violation. [README.md:89](/Volumes/MiniEx/projects/steer-skill/README.md:89) | A PreToolUse Python hook that enforces concrete patterns and command forms. [skills/steer/SKILL.md:10](/Volumes/MiniEx/projects/steer-skill/skills/steer/SKILL.md:10) |
| **harness** | none | A supported coding agent into which Steer installs hooks and from which it reads transcripts: Claude Code, Codex, or Pi. [CLAUDE.md:Harness registry section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **hook** | A guard is a Python hook, and hooks are enabled separately from the commit gate. [README.md:89](/Volumes/MiniEx/projects/steer-skill/README.md:89) | A handler invoked by an agent tool or lifecycle event. [CLAUDE.md:The guard store section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **instrument** | none | The reviewer, model, runs, prompt, and engine identity used to produce ledger rows. [CLAUDE.md:Reviewer configuration section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **intervention / interventions** | A recurring problem may call for a check, guard, instruction, or no change; Steer tracks interventions against baselines. [README.md:77](/Volumes/MiniEx/projects/steer-skill/README.md:77) | A recorded fix aimed at a recurring class and measured against a baseline. [skills/steer-report/SKILL.md:335](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:335) |
| **ledger** | The commit gate records reviews and adjudications locally. [README.md:68](/Volumes/MiniEx/projects/steer-skill/README.md:68) | The local record of adjudications; unresolved rows are joined to transcripts by the resolver. [CLAUDE.md:The resolver section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **master hook** | none | The generated hook that loads and runs all installed guards for a scope. [CLAUDE.md:The guard store section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **preset** | A bundled factory hook that can be listed or enabled. [README.md:91](/Volumes/MiniEx/projects/steer-skill/README.md:91) | none |
| **posture** | none | The read-only snapshot of whether Steer is armed, registered, resolvable, observed, and needs attention. [CLAUDE.md:The `steer status --json` contract section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **readiness** | none | Whether a commit-gate registration is usable after considering registration, command resolution, and observation. [CLAUDE.md:The `steer status --json` contract section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **rejected finding** | The agent reports findings it accepted or rejected. [README.md:62](/Volumes/MiniEx/projects/steer-skill/README.md:62) | A false-positive class can be recorded as a reviewer suppression. [skills/steer-report/SKILL.md:302](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:302) |
| **resolver** | none | The Stop-hook process that joins ledger rows to transcripts for scoring. [CLAUDE.md:The resolver section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **reviewer** | An installed, authenticated CLI that independently inspects a proposed change. [README.md:21](/Volumes/MiniEx/projects/steer-skill/README.md:21) | none |
| **scope** | Project or global scope selects where hook operations apply. [README.md:54](/Volumes/MiniEx/projects/steer-skill/README.md:54) | The frozen set of evidence opportunities included in a measurement. [skills/steer-report/SKILL.md:412](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:412) |
| **session** | none | The statistical unit for transcript friction: one root agent session; subagent threads are provenance. [skills/steer-report/SKILL.md:376](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:376) |
| **signal kind** | none | The transcript classifier category: corrections, rejected calls, tool failures, hook blocks, or any signal. [skills/steer-report/SKILL.md:376](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:376) |
| **tier** | none | The route selected by detectability: `check`, `steer`, `prompt`, or `prose`. [skills/steer-report/SKILL.md:308](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:308) |
| **tool failure** | A recurring lint, test, or compile failure found in agent transcripts. [cli/cmd/report.go:33](/Volumes/MiniEx/projects/steer-skill/cli/cmd/report.go:33) | none |
| **transcript** | Steer reads agent transcripts to find recurring corrections and tool failures. [README.md:68](/Volumes/MiniEx/projects/steer-skill/README.md:68) | A session record scanned for transcript signals. [skills/steer-report/SKILL.md:278](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:278) |
| **trust review** | Codex hook definitions must be reviewed and trusted in `/hooks`. [README.md:54](/Volumes/MiniEx/projects/steer-skill/README.md:54) | Registration alone does not prove enforcement; trust is a harness capability. [CLAUDE.md:Harness registry section](/Volumes/MiniEx/projects/steer-skill/CLAUDE.md) |
| **unit** | none | One denominator entry: a review chain or root transcript session. [skills/steer-report/SKILL.md:410](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:410) |
| **verdict** | Status reports measured verdicts. [cli/cmd/status.go:28](/Volumes/MiniEx/projects/steer-skill/cli/cmd/status.go:28) | The scorer's interpretation of before/after evidence, with labels such as `effective`, `harmful`, `ineffective`, `collecting`, and `inconclusive`. [skills/steer-report/SKILL.md:86](/Volumes/MiniEx/projects/steer-skill/skills/steer-report/SKILL.md:86) |

## Terms currently used without an explanation in either audience

| Term | User documentation | Agent documentation |
|---|---|---|
| **auto-denials corpus** | none | none |
| **matcher** | none | none |
