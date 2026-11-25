# Pi transcript fixtures

Every file here is synthetic, hand-built against the session format Pi ships in
`docs/session-format.md`, except `stopblock/check-repair.jsonl`: Pi 1.6.0 wrote it
in a sandbox during `tests/pi_author.py --case check`, and the sandbox path is
replaced by `/sandbox`.

The local corpus this adapter was written against holds only the ordinary case
(`normal.jsonl`): 37 sessions, none of them branched, forked, malformed, or
carrying a `bashExecution`, `custom` or unknown entry. Those are the rows that
decide whether a count is right, so each one is written here by hand.
