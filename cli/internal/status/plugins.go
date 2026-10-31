package status

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

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
}

// Differs reports a copy that bundles a build other than the stable executable.
func (p PluginCopy) Differs() bool { return p.State == PluginDifferentBinary }

// BinInstallCommand places this copy's build at the stable executable.
func (p PluginCopy) BinInstallCommand() string {
	return steerbin.Quote(filepath.Join(p.Path, "scripts", "steer")) + " bin install"
}

// PluginCopies reads every cached Steer plugin copy in the Claude Code and Codex
// global plugin caches. A harness with no cache yields no row. Claude Code keeps an
// updated plugin's earlier version directory, so a Claude Code copy counts only
// when its install record names it.
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
			bundled := filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH, steerpath.BinName)
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
		parts = append(parts, p.Harness+":"+p.Path)
		labels = append(labels, HarnessLabel(p.Harness)+" "+p.Path)
	}
	if len(parts) == 0 {
		return "", nil
	}
	sort.Strings(parts)
	return strings.Join(parts, "|"), labels
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
