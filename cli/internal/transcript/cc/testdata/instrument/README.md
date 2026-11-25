# instrument fixtures

The corpus `TestInstrumentMatchesFixtureReplay` replays to pin this adapter's
`instrument` constant. It is deliberately separate from the behaviour tests'
fixtures: adding a case to one of those must not read as a change to what the
adapter emits.

Editing a file here changes the digest, so edit one only to cover a shape the
adapter must keep handling, and bump the constant in the same change.
