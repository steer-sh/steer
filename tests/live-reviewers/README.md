# Live reviewer cells

`../live_reviewers.py` runs one selected provider through a real registered
Claude Code native gate command, using a throwaway home from `../sandbox.py`.
It builds the working-tree CLI unless `--steer-bin` selects a candidate binary.
Each cell costs two model calls: `test-reviewer`, then a staged change with a
planted arithmetic defect. It uses one reviewer per call.

From the repository root, with your sandbox login available:

```bash
python3 tests/live_reviewers.py --provider claude-code --model <model> --effort <effort> --output tests/live-reviewers/out/claude
python3 tests/live_reviewers.py --provider codex --model <model> --effort <effort> --output tests/live-reviewers/out/codex
python3 tests/live_reviewers.py --provider agy --model <model> --effort <effort> --output tests/live-reviewers/out/agy
```

Replace the model and effort placeholders with IDs your reviewer CLI accepts;
use `--effort ''` to omit effort. Claude Code requires
`CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` exported. Codex and agy use the
login copies supplied by `sandbox.py`. Choose a new output directory for each
run. The default output location above is ignored by git.

`result.json` holds the gate wall time, reviewer identity, ledger rows, session
paths and failures. Each `call-*` directory holds the actual argv (agy's prompt
argument is replaced by a pointer to `prompt.txt`), prompt, raw stdout/stderr,
return code and CLI wall time.

Output streams go directly to evidence files, retaining flushed partial output
when the gate kills a timed-out reviewer. An interrupted capture records an
unknown return code and an elapsed-time upper bound observed by the runner.
The trial has no ledger row; the live review must
deny the planted defect and write a row with the selected identity. Isolation
flags and prompt transport are checked from captured argv. The adopted fixture
repository and sandbox Stop markers check for inherited hook execution.

Claude Code and Codex must save no reviewer session; agy's session locations
are recorded. Real-home changes and live audit/ledger leaks fail the cell.
The default cleanup removes the sandbox home and copied logins; `--keep`
retains them for diagnosis. Evidence in the output directory survives cleanup.

Read the raw live reply to confirm that its finding identifies the planted
defect. The mechanical check requires the file and arithmetic failure wording;
it does not adjudicate the reasoning. Cost is reported as unmeasured because
the CLIs do not supply one shared cost field; raw replies retain any usage
fields a provider emits. This runner exercises the registered gate command
directly. Interactive harness delivery and hook approvals remain install-cell
checks.

`python3 tests/test_release_tools.py` verifies the runner offline against fake
CLIs and the real gate, including rejection of a clean reply for the defective
change. It runs in `check.sh`; live calls do not.
