// Package version carries the build identity the linker stamps in. It is a leaf
// package so any layer can name the running binary without importing cmd.
package version

// Version is the CLI's release identity, set with -X at build time.
var Version = "dev"

// GitCommit is the short commit the binary was built from.
var GitCommit = "unknown"

// BuildDate is the build's UTC timestamp.
var BuildDate = "unknown"
