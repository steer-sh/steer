# Pi transcript fixtures

Every file here is synthetic, hand-built against the session format Pi ships in
`docs/session-format.md`. None is a copy of a real session.

The local corpus this adapter was written against holds only the ordinary case
(`normal.jsonl`): 37 sessions, none of them branched, forked, malformed, or
carrying a `bashExecution`, `custom` or unknown entry. Those are the rows that
decide whether a count is right, so each one is written here by hand.
