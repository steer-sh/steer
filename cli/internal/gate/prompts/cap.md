The commit gate chain for this commit has ENDED at its {cap}-firing cap, and this message does not block anything — your commit is proceeding.

Round {cap}'s fixes changed the diff, so they are code no reviewer had read. Rather than leave them unreviewed, {count} reviewers ({reviewer}) read the current diff one final time. Their raw output is below.

Read the reviews the same way you read a fired gate: state each distinct concern's load-bearing premise, test THAT premise against the code, and rule on it. The reviewers saw the whole diff, not just round {cap}'s changes, so concerns you already adjudicated in earlier rounds can reappear — name those as repeats and move on.

--- REVIEWS ---
{review}
--- END REVIEWS ---

Two things follow from this being terminal rather than blocking:

1. There is no retry to fold fixes into. Apply the same repair policy: make a follow-up commit only for a defect whose repair a requirement needs, and the requirement must exist before this change: the user's request, or code, docs or tests at the base commit. This change's own commit message, comments or tests never count. Record other true concerns as ACCEPTED, not fixed. Acceptance or a BLOCKING label alone does not require a follow-up.
2. The user sees only a one-line count of these findings, never the text. So the ledger is how the user learns what they said and what you did about it, and it is mandatory here exactly as it is on a fired gate. The chain is over and this commit is proceeding, so THIS reply is where the chain ledger goes — one section per round the chain fired, in order, ending with this terminal round. Copy the header line as it stands, sequence ID and subject included: it is what files this ledger against THIS commit, and one turn often commits more than once to the same repo. Every line self-contained:

**Commit gate fired — review sequence{chain}, {subject}**

**Round 1, blocking** — <N> reviewers, <M> raw findings, <K> distinct concerns.

- **ACCEPTED** — <the concern in one clause> → needed by <the requirement>; <the change you made, file:line>
- **REJECTED** — <the concern in one clause> → <the premise, and why it is false>
- Weakest surviving: <the concern> — <why it still stands, or that none survived>

- one section in this shape for every round that fired, then:

**Round {firing}, terminal** — <N> reviewers, <M> raw findings, <K> distinct concerns. Commit not blocked.

- **ACCEPTED** — <the concern in one clause> → needed by <the requirement>; <the fix, and whether it needs a follow-up commit>
- **ACCEPTED, not fixed** — <the concern in one clause> → <why no supported use needs the repair, or the user's instruction>
- **REJECTED** — <the concern in one clause> → <the premise, and why it is false>
- Weakest surviving: <the concern> — <why it still stands, or that none survived>

A finding whose premise holds is ACCEPTED even when you leave it unfixed. REJECTED means its premise is false, never that fixing it is out of scope.

A round with no section reads as a round you never adjudicated, so the number of sections must equal the number of rounds that fired.

Do NOT put this ledger in a commit message.
