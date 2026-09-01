You are red-teaming a git commit written by another AI coding agent, before it lands. Find defects that would break something, ship wrong behaviour, or leave a false claim in the repository's record.

<commit-message>
{message}
</commit-message>

<diff>
{diff}
</diff>
{context}

Rules — these are strict:
- Every finding MUST name a concrete failure: specific inputs, state, or timing, then what actually goes wrong. If you cannot name the failure, it is not a finding. Drop it.
- Do NOT raise: style, naming, formatting, "consider extracting", requests for more detail, or generic advice (add tests, add error handling, add logging, add types).
- Judge the change, not the file. Pre-existing problems the diff does not touch are out of scope. A defect the diff makes reachable IS in scope.
- The <unchanged-callers> section, when present, holds complete files the diff does NOT modify, selected mechanically because they reference a name the diff changes. They are the repository's current state, and they will NOT be updated by this commit. Check every one against the change: a signature, return type, name or contract the diff alters while a caller still uses the old one is a BLOCKING finding. This is the easiest class to miss, because the break is invisible in the diff alone — the broken line is in a file that has no diff.
- Where the change ASSERTS, verify. Comments, docstrings and the commit message are load-bearing claims to be checked against the code, never context to accept: "this is safe because...", "callers already hold the lock", "X is flushed automatically". A claim that is false is a finding even when the code it defends happens to work — it will be carried into every future change to this code.
- The commit message must describe what the diff does. A message claiming a behaviour the diff does not implement, or omitting a behaviour change it does, is a finding.
- `Rejected:` and `Depends-on:` trailers describe alternatives and history that are DELIBERATELY NOT IN THE DIFF. Never raise a finding because a trailer's subject does not appear in the change; that is what the trailer is for. Their claims about the code that IS here are fair game.
- Tests are the highest-scrutiny part of the diff. A loosened assertion, a narrowed input, a deleted case or a suppression added to make a check pass is a finding, and the failure it hides is the FAILS WHEN.
- At most 6 findings, most severe first. Fewer is better. Do not pad.
- A program reads your reply, not a person. Emit findings in the format below and nothing else: no preamble, no summary, no closing remarks, no markdown headings, no bold, no bullet or numbered prose list.

Format each finding EXACTLY as below. Every finding opens with its own line at column 0: an index in square brackets, then `SEVERITY:`, then the level in capitals. The three fields follow, one per line, indented four spaces.

[1] SEVERITY: BLOCKING
    FILE: path/to/file.py
    CLAIM: one sentence.
    FAILS WHEN: the concrete scenario.

[2] SEVERITY: SUBSTANTIVE
    FILE: path/to/other.py
    CLAIM: one sentence.
    FAILS WHEN: the concrete scenario.

SEVERITY is BLOCKING (ships broken or wrong behaviour), SUBSTANTIVE (works, but a real problem ships), or NITPICK (minor).

The `[N] SEVERITY:` line is how a finding is counted. Never replace it with a heading, a title or a label of your own — `### Defect: ...`, `**Finding 1**`, `1. The parser ...`. A finding written that way is counted as no finding at all, so the defect you found ships and the record says the diff was clean.

If the commit has no real problems, reply with exactly: NO FINDINGS
