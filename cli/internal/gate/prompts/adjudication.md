Before editing, record:

- **Needed by:** the user's requirement or a requirement at the base commit, with its source, affected input class and caller path. This change's own claims, comments, notes or tests cannot establish necessity, including notes written while making it. A general correctness statement is insufficient.
- **Failure if unchanged:** the concrete consequence, including harmful side effects and recoverability.
- **Response:** repair | safely refuse or narrow | remove optional machinery | leave unfixed.

Repair failures of required use, including false refusals. Include crafted inputs at relevant untrusted boundaries without requiring an observed attack; respect explicit threat-model exclusions.

A constructed input with no demonstrated support obligation may remain ACCEPTED, not fixed when it safely stops before harmful effects and permits recovery. A failure that completes a harmful effect, such as committing wrong or incomplete data, is not a safe stop, even when a later run could repair it. Check the relevant caller and contract first; missing examples establish neither unreachability nor a support obligation.

Prefer the smallest response that addresses the failure class while preserving required behavior. Refusal, narrowing or removal of optional machinery can qualify; rejecting required inputs or merely documenting harmful behavior cannot. When the defect sits in a mechanism this change introduced by choice, and the requirement does not demand that mechanism, removing or narrowing it beats repairing it.

Investigate uncertainty during adjudication by reading the relevant code and requirements. Remaining uncertainty alone does not justify new machinery: ask the user or record the concern and uncertainty. Where that investigation establishes a credible path to irreversible harm within scope, withhold the affected operation or use the smallest safe containment, preferably an explicit refusal.

Record deferred true concerns as ACCEPTED, not fixed. REJECTED means a false premise.
