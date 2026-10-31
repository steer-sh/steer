package status

import (
	"debug/buildinfo"
	"regexp"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/version"
)

// BuildIdentity is the version and commit a binary was stamped with.
type BuildIdentity struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// Known reports a binary whose stamps could be read.
func (b BuildIdentity) Known() bool { return b.Version != "" && b.Commit != "" }

// Release reports a build stamped with a release version. Only releases have an order.
func (b BuildIdentity) Release() bool {
	_, ok := parseVersion(b.Version)
	return ok
}

// Label names a build for a reader: its version alone for a release, with the commit
// for a development build.
func (b BuildIdentity) Label() string {
	switch {
	case !b.Known():
		return "unknown build"
	case b.Release():
		return b.Version
	}
	return b.Version + " (" + b.Commit + ")"
}

// DetailLabel always names the commit.
func (b BuildIdentity) DetailLabel() string {
	if !b.Known() {
		return "unknown build"
	}
	return b.Version + " (" + b.Commit + ")"
}

var (
	stampVersion = regexp.MustCompile(`version\.Version=([^' ]+)`)
	stampCommit  = regexp.MustCompile(`version\.GitCommit=([^' ]+)`)
)

// ReadBuild reads a binary's stamps from its build information without running it. An
// unreadable or unstamped binary yields a zero identity.
func ReadBuild(path string) BuildIdentity {
	if path == "" {
		return BuildIdentity{}
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return BuildIdentity{}
	}
	for _, setting := range info.Settings {
		if setting.Key != "-ldflags" {
			continue
		}
		v, c := stampVersion.FindStringSubmatch(setting.Value), stampCommit.FindStringSubmatch(setting.Value)
		if v != nil && c != nil {
			return BuildIdentity{Version: v[1], Commit: c[1]}
		}
	}
	return BuildIdentity{}
}

// RunningBuild is this process's own identity.
func RunningBuild() BuildIdentity {
	return BuildIdentity{Version: version.Version, Commit: version.GitCommit}
}

// HooksBuild is the identity of the stable executable the hooks run.
func HooksBuild() BuildIdentity {
	path, err := steerbin.Path()
	if err != nil {
		return BuildIdentity{}
	}
	return ReadBuild(path)
}

// parseVersion reads a plain major.minor.patch version. ok is false for anything else,
// which has no order.
func parseVersion(v string) (out [3]int, ok bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// compareVersions orders two parsed versions: negative when a is older.
func compareVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

// How a plugin copy's build stands against the build the hooks run.
const (
	BuildSame      = "same"
	BuildOlder     = "older"
	BuildNewer     = "newer"
	BuildConflict  = "conflict"
	BuildUnordered = "unordered"
	BuildUnknown   = "unknown"
)

// CompareToHooks reads this copy against the hooks' build. Releases order by version;
// a difference involving a development build has no known order; one version with two
// commits is a conflict.
func (p PluginCopy) CompareToHooks(hooks BuildIdentity) string {
	if p.State == PluginSameBinary {
		return BuildSame
	}
	own := BuildIdentity{Version: p.Version, Commit: p.Commit}
	if p.State == PluginUnknown || !own.Known() || !hooks.Known() {
		return BuildUnknown
	}
	if !own.Release() || !hooks.Release() {
		return BuildUnordered
	}
	a, _ := parseVersion(own.Version)
	b, _ := parseVersion(hooks.Version)
	switch order := compareVersions(a, b); {
	case order < 0:
		return BuildOlder
	case order > 0:
		return BuildNewer
	case own.Commit == hooks.Commit:
		return BuildSame
	}
	return BuildConflict
}
