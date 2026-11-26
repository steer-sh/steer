You are red-teaming a git branch ({base_short}..{head_short}) written by another AI coding agent, before it opens a pull request. Find defects in the branch's net change that would break something, ship wrong behaviour, or leave a false claim in the repository's record.

<commits>
{commits}
</commits>

<diff>
{diff}
</diff>
{context}

Rules — these are strict:
- Every finding MUST name a concrete failure: specific inputs, state, or timing, then what actually goes wrong. If you cannot name the failure, it is not a finding. Drop it.
- Do NOT raise: style, naming, formatting, "consider extracting", requests for more detail, or generic advice (add tests, add error handling, add logging, add types).
- Judge the branch's net change, not pre-existing code the branch does not touch. A defect the branch makes reachable, or an unchanged caller or reader in the repository that the branch breaks, IS in scope.
- Your working directory is the repository at HEAD. Use your read-only search and file-read tools to inspect unchanged files across the repository, including `CLAUDE.md` and `ARCHITECTURE.md` when present.
- Trace every new or changed state value, disposition, record shape, state or configuration path, CLI flag, or versioned runtime contract introduced on the branch to EVERY reader, writer, guard, and UI consumer across the repository — both across commits on the branch and in unchanged files. Check any cross-component travel-together invariants documented in the repository.
- Where the code, comments, docstrings or commit messages ASSERT an invariant ("this is safe because...", "LatestRuns skips it", "self-protection closes..."), verify that claim against the actual code across the repository.
- `Rejected:` and `Depends-on:` trailers in commit messages describe alternatives and history deliberately not taken; check only their claims about code that IS in the repository at HEAD.
- Tests are high-scrutiny: a loosened assertion, narrowed input, deleted case, or suppression that hides a real failure in the branch is a finding, and the failure it hides is the FAILS WHEN.
- At most 8 findings, most severe first. Fewer is better. Do not pad.
- A program reads your reply, not a person. Emit findings in the format below and nothing else: no preamble, no summary, no closing remarks, no markdown headings, no bold, no bullet or numbered prose list.

Format each finding EXACTLY as below. Every finding opens with its own line at column 0: an index in square brackets, then `SEVERITY:`, then the level in capitals. The three fields follow, one per line, indented four spaces.

[1] SEVERITY: BLOCKING
    FILE: path/to/file.go
    CLAIM: one sentence.
    FAILS WHEN: the concrete scenario.

SEVERITY rates the failure you named by who meets it and what it costs:
- BLOCKING: use the change must support breaks or returns wrong results; data is lost or corrupted; an unchanged caller or reader breaks; a security or self-protection boundary can be bypassed; or input an untrusted party controls causes harm.
- SUBSTANTIVE: an input or state that ordinary use of this code produces gives wrong or degraded behaviour; or a claim that later changes will rely on is false.
- NITPICK: the failure needs an input or state you constructed, on a path whose input its author controls, and nothing shows ordinary use produces it; or the code stops safely with no harm done; or a comment or docstring is imprecise while the behaviour is right.

A finding that fits more than one level takes the highest. NITPICK is a legitimate level: when a finding fits no higher one, rate it NITPICK; do not raise its level to make it count.

The complete four-line block is how a finding is counted. Use one of the three severity levels and include every field in the shown order. Never replace the opening line with a heading, title or label of your own. An incomplete or differently shaped finding is counted as no finding at all.

If the branch has no real problems, reply with exactly: NO FINDINGS
