The commit gate chain for this commit has ENDED at its {cap}-firing cap, and this message does not block anything — your commit is proceeding.

Round {cap}'s fixes changed the diff, so they are code no reviewer had read. Rather than leave them unreviewed, {count} reviewers ({reviewer}) read the current diff one final time. Their raw output is below.

Read the reviews the same way you read a fired gate: state each distinct concern's load-bearing premise, test THAT premise against the code, and rule on it. The reviewers saw the whole diff, not just round {cap}'s changes, so concerns you already adjudicated in earlier rounds can reappear — name those as repeats and move on.

--- REVIEWS ---
{review}
--- END REVIEWS ---

Two things follow from this being terminal rather than blocking:

1. There is no retry to fold fixes into. Apply this policy before deciding on a follow-up commit; reaching the cap creates no repair obligation. Make a follow-up commit only for a concern whose response is a repair, a refusal or narrowing, or a removal. Acceptance or a BLOCKING label alone does not require a follow-up.

   Before editing, record:

   - **Needed by:** the user's requirement or a requirement at the base commit, with its source, affected input class and caller path. This change's own claims, comments, notes or tests cannot establish necessity, including notes written while making it. A general correctness statement is insufficient.
   - **Failure if unchanged:** the concrete consequence, including harmful side effects and recoverability.
   - **Response:** repair | safely refuse or narrow | remove optional machinery | leave unfixed.

   Repair failures of required use, including false refusals. Include crafted inputs at relevant untrusted boundaries without requiring an observed attack; respect explicit threat-model exclusions.

   A constructed input with no demonstrated support obligation may remain ACCEPTED, not fixed when it safely stops before harmful effects and permits recovery. A failure that completes a harmful effect, such as committing wrong or incomplete data, is not a safe stop, even when a later run could repair it. Check the relevant caller and contract first; missing examples establish neither unreachability nor a support obligation.

   Prefer the smallest response that addresses the failure class while preserving required behavior. Refusal, narrowing or removal of optional machinery can qualify; rejecting required inputs or merely documenting harmful behavior cannot. When the defect sits in a mechanism this change introduced by choice, and the requirement does not demand that mechanism, removing or narrowing it beats repairing it.

   Investigate uncertainty during adjudication by reading the relevant code and requirements. Remaining uncertainty alone does not justify new machinery: ask the user or record the concern and uncertainty. Where that investigation establishes a credible path to irreversible harm within scope, withhold the affected operation or use the smallest safe containment, preferably an explicit refusal.

   Record deferred true concerns as ACCEPTED, not fixed. REJECTED means a false premise.

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
