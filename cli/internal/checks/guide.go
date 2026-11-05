package checks

import _ "embed"

//go:embed guide.md
var guide string

// Guide returns the embedded instructions for authoring repository checks.
func Guide() string { return guide }
