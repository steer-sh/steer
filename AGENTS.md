## Steer Rules
<!-- managed-by-steer -->
- never add a Go test that nearly copies another test in the same file | add the case as a row in the twin test or a shared helper; if the test is distinct, say what it checks in its doc comment and retry
- never commit on main or push to main in this repository | work on a branch and merge through a pull request
- never change the repository-root README.md | the maintainer edits README.md by hand; tell the user what change it needs and quote the text
- When you add another variant of an adapter, stream, store, bridge, runner or command (a harness, a reviewer, a JSONL stream, a test runner, a subcommand), move the code it shares with the existing variants into one helper, and keep in each variant only what differs. Do not copy an existing variant's body. A copy that a test requires to stay identical is exempt.
<!-- end-managed-by-steer -->
