package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/codex"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/mods"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/steerbin"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Plugin copy states. A harness installs a plugin by copying its root into a cache,
// and the skills and the launcher it runs come from that copy, not from the stable
// executable the hooks name.
const (
	// PluginSameBinary means the copy bundles the stable executable's exact bytes.
	PluginSameBinary = "same-binary"
	// PluginDifferentBinary means the copy bundles another build, so its skills and
	// its launcher come from another build too.
	PluginDifferentBinary = "different-binary"
	// PluginUnknown means one of the two binaries could not be read.
	PluginUnknown = "unknown"
)

// codexLocalVersion is the version directory Codex prefers over any numbered one.
const codexLocalVersion = "local"

// pluginName is the directory a harness caches the Steer plugin under.
const pluginName = "steer"

// PluginCopy is one harness's cached copy of the Steer plugin, read against the
// stable executable.
type PluginCopy struct {
	Harness string `json:"harness"`
	// Path is the copy's root in the harness's plugin cache.
	Path string `json:"path"`
	// Target is where Path resolves when it is a symbolic link, "" otherwise.
	Target string `json:"target,omitempty"`
	State  string `json:"state"`
	// Version and Commit are the stamps of the copy's bundled binary.
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// Differs reports a copy that bundles a build other than the stable executable.
func (p PluginCopy) Differs() bool { return p.State == PluginDifferentBinary }

// Binary is the executable this copy bundles for this platform.
func (p PluginCopy) Binary() string {
	return filepath.Join(p.Path, "bin", runtime.GOOS+"-"+runtime.GOARCH, steerpath.BinName)
}

// BinInstallCommand places this copy's build at the stable executable.
func (p PluginCopy) BinInstallCommand() string {
	return steerbin.Quote(filepath.Join(p.Path, "scripts", "steer")) + " bin install"
}

// PluginCopies reads the active cached Steer plugin copy in the Claude Code and Codex
// global plugin caches. A harness with no cache yields no row. Both keep an updated
// plugin's earlier version directory: a Claude Code copy counts only when its install
// record names it, and a Codex copy only when it is the version Codex loads.
func PluginCopies() []PluginCopy {
	stable, _ := steerbin.Path()
	out := []PluginCopy{}
	for _, id := range []harness.ID{harness.ClaudeCode, harness.Codex} {
		h, ok := harness.Get(id)
		if !ok {
			continue
		}
		dir, err := h.ConfigDir(harness.GlobalScope, "")
		if err != nil {
			continue
		}
		roots, _ := filepath.Glob(filepath.Join(dir, "plugins", "cache", "*", pluginName, "*"))
		sort.Strings(roots)
		if id == harness.Codex {
			roots = codexActiveRoots(roots)
		}
		installed, recorded := map[string]bool{}, false
		if id == harness.ClaudeCode {
			installed, recorded = claudeInstallPaths(dir)
		}
		for _, root := range roots {
			if recorded && !installed[filepath.Clean(root)] {
				continue
			}
			info, err := os.Stat(root)
			if err != nil || !info.IsDir() {
				continue
			}
			row := PluginCopy{Harness: string(id), Path: root, State: PluginUnknown}
			if link, err := os.Lstat(root); err == nil && link.Mode()&os.ModeSymlink != 0 {
				if target, err := filepath.EvalSymlinks(root); err == nil {
					row.Target = target
				}
			}
			bundled := row.Binary()
			identity := ReadBuild(bundled)
			row.Version, row.Commit = identity.Version, identity.Commit
			if same, ok := sameFile(bundled, stable); ok {
				row.State = PluginDifferentBinary
				if same {
					row.State = PluginSameBinary
				}
			}
			out = append(out, row)
		}
	}
	return out
}

// codexActiveRoots keeps, per marketplace, the version directory Codex loads: "local"
// when present, otherwise the highest version.
func codexActiveRoots(roots []string) []string {
	best := map[string]string{}
	for _, root := range roots {
		parent := filepath.Dir(root)
		current, ok := best[parent]
		if !ok || codexVersionWins(filepath.Base(root), filepath.Base(current)) {
			best[parent] = root
		}
	}
	out := make([]string, 0, len(best))
	for _, root := range best {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func codexVersionWins(candidate, current string) bool {
	if candidate == codexLocalVersion || current == codexLocalVersion {
		return candidate == codexLocalVersion
	}
	a, aOK := parseVersion(candidate)
	b, bOK := parseVersion(current)
	if aOK && bOK {
		return compareVersions(a, b) > 0
	}
	if aOK != bOK {
		return aOK
	}
	return candidate > current
}

// claudeInstallPaths returns the install paths Claude Code's plugin record names for
// the Steer plugin from any marketplace. ok is false when the record is missing or
// unreadable.
func claudeInstallPaths(configDir string) (paths map[string]bool, ok bool) {
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	data, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(data, &installed) != nil {
		return nil, false
	}
	paths = map[string]bool{}
	for id, records := range installed.Plugins {
		if name, _, _ := strings.Cut(id, "@"); name != pluginName {
			continue
		}
		for _, r := range records {
			if r.InstallPath != "" {
				paths[filepath.Clean(r.InstallPath)] = true
			}
		}
	}
	return paths, true
}

// PluginSources names each harness whose plugin cache holds a path the stable
// executable was installed from.
func PluginSources() []harness.ID {
	var out []harness.ID
	for _, id := range []harness.ID{harness.ClaudeCode, harness.Codex} {
		if len(sourcesIn(id)) > 0 {
			out = append(out, id)
		}
	}
	return out
}

// sourcesIn returns the recorded install sources that lie in the harness's plugin cache.
func sourcesIn(id harness.ID) []string {
	h, ok := harness.Get(id)
	if !ok {
		return nil
	}
	dir, err := h.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return nil
	}
	cache := filepath.Join(dir, "plugins", "cache")
	var out []string
	for _, source := range steerbin.Sources() {
		if lexicallyWithin(cache, source) || steerpath.Within(cache, source) {
			out = append(out, source)
		}
	}
	return out
}

// ForgetPluginSource drops the recorded sources that lie in the harness's plugin cache.
func ForgetPluginSource(id harness.ID) error {
	drop := map[string]bool{}
	for _, source := range sourcesIn(id) {
		drop[source] = true
	}
	var kept []string
	for _, source := range steerbin.Sources() {
		if !drop[source] {
			kept = append(kept, source)
		}
	}
	return steerbin.SetSources(kept)
}

// RemovedPlugins names each harness the stable executable was installed from whose
// own install record no longer names the Steer plugin while its global configuration
// still calls Steer. Its hooks still run
// ~/.steer/bin/steer, so nothing else tells the operator that Steer is still active.
func RemovedPlugins() []harness.ID {
	var out []harness.ID
	for _, id := range PluginSources() {
		h, _ := harness.Get(id)
		dir, err := h.ConfigDir(harness.GlobalScope, "")
		if err == nil && !pluginInstalled(id, dir) && globallyRegistered(id) {
			out = append(out, id)
		}
	}
	return out
}

// PluginRemovedNotice is the operator's line for each of RemovedPlugins, or "".
func PluginRemovedNotice() string {
	var notes []string
	for _, id := range RemovedPlugins() {
		notes = append(notes, fmt.Sprintf("[steer] The Steer plugin is no longer installed in %s, but Steer's hooks "+
			"still run. To remove them: %s. To keep them: ~/.steer/bin/steer bin install.",
			HarnessLabel(string(id)), UninstallCommand(id)))
	}
	return strings.Join(notes, " ")
}

// globallyRegistered reports whether the harness's global configuration still calls
// Steer: a commit gate, resolver, check runner, guard runner, or Steer's status line.
func globallyRegistered(id harness.ID) bool {
	g := harness.GlobalScope
	switch id {
	case harness.ClaudeCode:
		if claude.CommitGateHookArmed(g).Registered || claude.BackfillHookArming(g).Registered {
			return true
		}
		if a, err := claude.CheckHookArming(g); err == nil && a.Registered {
			return true
		}
		if registered, err := (claude.Registrar{}).MasterRegistered(g); err == nil && registered {
			return true
		}
		_, present, mine := claude.StatusLineArmed(g)
		return present && mine
	case harness.Codex:
		return codex.CommitGateArming(g, "").Registered || codex.ResolverArmingRow(g, "").Registered ||
			codex.CheckHookArming(g, "").Registered || codex.MasterArming(g, "").Registered
	}
	return false
}

// PluginInstalled reports whether the harness's own install record names the Steer
// plugin; a cache directory alone can outlive the plugin.
func PluginInstalled(id harness.ID) bool {
	h, ok := harness.Get(id)
	if !ok {
		return false
	}
	dir, err := h.ConfigDir(harness.GlobalScope, "")
	return err == nil && pluginInstalled(id, dir)
}

// UninstallCommand removes one harness's global Steer registrations.
func UninstallCommand(id harness.ID) string {
	return "~/.steer/bin/steer uninstall --harness " + string(id) + " --apply"
}

// pluginInstalled reads the harness's own record of installed plugins: Claude Code's
// installed_plugins.json, and the plugins table of Codex's config.toml. A record that
// exists but cannot be read counts as installed, so a read failure raises no notice.
func pluginInstalled(id harness.ID, dir string) bool {
	if id == harness.ClaudeCode {
		paths, ok := claudeInstallPaths(dir)
		if !ok {
			_, err := os.Stat(filepath.Join(dir, "plugins", "installed_plugins.json"))
			return !os.IsNotExist(err)
		}
		return len(paths) > 0
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		return !os.IsNotExist(err)
	}
	var config struct {
		Plugins map[string]any `toml:"plugins"`
	}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return true
	}
	for key := range config.Plugins {
		if strings.HasPrefix(key, pluginName+"@") {
			return true
		}
	}
	return false
}

func lexicallyWithin(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ModExposures reads each enabled Claude Code mod against the inspection cache that
// `steer mods inspect` writes. It runs no process, so a mod nobody inspected reads as
// uninspected rather than as safe.
func ModExposures() []mods.Exposure {
	h, ok := harness.Get(harness.ClaudeCode)
	if !ok {
		return []mods.Exposure{}
	}
	dir, err := h.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return []mods.Exposure{}
	}
	root, _ := steerpath.ProjectRoot()
	return mods.Read(mods.Candidates(dir, root), statestore.Resolve(mods.Component))
}

// DifferingPlugins summarizes the copies that bundle another build for a one-line
// notice: a key that changes whenever the set does, and one label per copy.
func DifferingPlugins(rows []PluginCopy) (key string, labels []string) {
	var parts []string
	for _, p := range rows {
		if !p.Differs() {
			continue
		}
		part := p.Harness + ":" + p.Path
		if p.Version != "" {
			// A copy rebuilt in place can change direction at the same path.
			part += "@" + p.Version + ":" + p.Commit
		}
		parts = append(parts, part)
		labels = append(labels, HarnessLabel(p.Harness)+" "+p.Path)
	}
	if len(parts) == 0 {
		return "", nil
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), labels
}

// ActionablePlugins keeps the copies worth a startup notice: a difference from the hooks'
// build with a known direction, one that breaks release identity, or a differing binary
// whose stamps cannot be read. A development build's difference asks nothing.
func ActionablePlugins(rows []PluginCopy, hooks BuildIdentity) []PluginCopy {
	var out []PluginCopy
	for _, p := range rows {
		switch p.CompareToHooks(hooks) {
		case BuildOlder, BuildNewer, BuildConflict:
			out = append(out, p)
		case BuildUnknown:
			if p.Differs() {
				out = append(out, p)
			}
		}
	}
	return out
}

// sameFile compares two files byte for byte. ok is false when either cannot be read.
func sameFile(a, b string) (same, ok bool) {
	if a == "" || b == "" {
		return false, false
	}
	ia, err := os.Stat(a)
	if err != nil {
		return false, false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, false
	}
	if ia.Size() != ib.Size() {
		return false, true
	}
	fa, err := os.Open(a)
	if err != nil {
		return false, false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, false
	}
	defer fb.Close()
	bufA := make([]byte, 64<<10)
	bufB := make([]byte, 64<<10)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, true
		}
		if errA == io.EOF || errA == io.ErrUnexpectedEOF {
			return errB == io.EOF || errB == io.ErrUnexpectedEOF, true
		}
		if errA != nil || errB != nil {
			return false, false
		}
	}
}
