## Steer Rules
<!-- managed-by-steer -->
- never add a Go test that nearly copies another test in the same file | add the case as a row in the twin test or a shared helper; if the test is distinct, say what it checks in its doc comment and retry
- never commit on main or push to main in this repository | work on a branch and merge through a pull request
- never change the repository-root README.md | the maintainer edits README.md by hand; tell the user what change it needs and quote the text
<!-- end-managed-by-steer -->
