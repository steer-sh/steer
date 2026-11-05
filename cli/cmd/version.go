package cmd

import (
	"fmt"

	"github.com/steer-sh/steer-cli/internal/version"
)

// versionLine names the running build: the release version, which a local build
// stamps as "dev", then the commit and build date that tell two builds apart.
func versionLine() string {
	return fmt.Sprintf("steer %s (%s, built %s)", version.Version, version.GitCommit, version.BuildDate)
}

func init() {
	rootCmd.Version = version.Version
	rootCmd.SetVersionTemplate(versionLine() + "\n")
}
