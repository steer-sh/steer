// Package mods reads which enabled Claude Code plugins carry a mod that can skip or
// override Steer's hooks. A mod's tool.call handler runs before every PreToolUse hook
// a settings file registers and can answer a call so those hooks never run; its
// tool.check handler can approve a call such a hook blocked. Inspect asks Claude
// Code's own validator which events each mod handles and caches the answer; Read
// joins that cache to the plugins enabled now and starts no process.
package mods

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Component is the statestore component that holds the inspection cache.
const Component = "mods"

const cacheFile = "inspections.json"

// Exposure states.
const (
	// StateToolHooks means the mod handles a tool event, so it can skip or override
	// Steer's hooks.
	StateToolHooks = "tool-hooks"
	// StateNoToolHooks means the inspection found no tool event handler.
	StateNoToolHooks = "no-tool-hooks"
	// StateUninspected means no inspection exists for the mod.
	StateUninspected = "uninspected"
	// StateStale means the mod's files changed since its inspection.
	StateStale = "stale"
	// StateFailed means the validator refused the mod or could not run.
	StateFailed = "failed"
)

// fingerprintLimit bounds the files a fingerprint stats, so a plugin that ships a
// dependency tree cannot slow a status read.
const fingerprintLimit = 2000

// Candidate is one enabled plugin whose hooks/hooks.json names a hooks module.
type Candidate struct {
	ID   string
	Path string
	// Enabled is the settings scope whose enabledPlugins entry decided the plugin is
	// on: user, project or local.
	Enabled string
}

// Inspection is one validator reading of a mod, keyed by the files it read.
type Inspection struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Fingerprint string   `json:"fingerprint"`
	Claude      string   `json:"claude_version,omitempty"`
	InspectedAt int64    `json:"inspected_at"`
	Hooks       []string `json:"hooks"`
	Error       string   `json:"error,omitempty"`
}

// Exposure is one enabled mod read against its cached inspection.
type Exposure struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Enabled string `json:"enabled"`
	State   string `json:"state"`
	// Handlers are the tool event handlers the inspection found, with their filters.
	Handlers    []string `json:"handlers,omitempty"`
	InspectedAt int64    `json:"inspected_at,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// CanBypass reports a mod that handles a tool event.
func (e Exposure) CanBypass() bool { return e.State == StateToolHooks }

// Inspected reports a mod whose cached inspection matches its current files.
func (e Exposure) Inspected() bool {
	return e.State == StateToolHooks || e.State == StateNoToolHooks
}

type installRecord struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	ProjectPath string `json:"projectPath"`
}

// Candidates lists the enabled plugins with a hooks module. configDir is Claude
// Code's global configuration directory; repoRoot is the repository whose project
// and local settings apply, or "" outside one. Built-in plugins have no install
// record and are not listed.
func Candidates(configDir, repoRoot string) []Candidate {
	var installed struct {
		Plugins map[string][]installRecord `json:"plugins"`
	}
	data, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(data, &installed) != nil {
		return nil
	}
	enabled := map[string]bool{}
	from := map[string]string{}
	layers := []struct{ scope, path string }{{"user", filepath.Join(configDir, "settings.json")}}
	if repoRoot != "" {
		layers = append(layers,
			struct{ scope, path string }{"project", filepath.Join(repoRoot, ".claude", "settings.json")},
			struct{ scope, path string }{"local", filepath.Join(repoRoot, ".claude", "settings.local.json")})
	}
	for _, layer := range layers {
		for id, on := range enabledPlugins(layer.path) {
			enabled[id] = on
			from[id] = layer.scope
		}
	}
	var out []Candidate
	for id, on := range enabled {
		if !on {
			continue
		}
		path := installPath(installed.Plugins[id], repoRoot)
		if path == "" || !hasModule(path) {
			continue
		}
		out = append(out, Candidate{ID: id, Path: path, Enabled: from[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func enabledPlugins(path string) map[string]bool {
	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &settings) != nil {
		return nil
	}
	out := map[string]bool{}
	for id, v := range settings.EnabledPlugins {
		out[id] = v == true
	}
	return out
}

// installPath picks the install a session in repoRoot loads: one bound to that
// project first, then a user install.
func installPath(records []installRecord, repoRoot string) string {
	user := ""
	for _, r := range records {
		if r.InstallPath == "" {
			continue
		}
		if r.ProjectPath != "" {
			if repoRoot != "" && filepath.Clean(r.ProjectPath) == filepath.Clean(repoRoot) {
				return r.InstallPath
			}
			continue
		}
		if user == "" {
			user = r.InstallPath
		}
	}
	return user
}

func hasModule(root string) bool {
	var hooks struct {
		Modules []string `json:"modules"`
	}
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil || json.Unmarshal(data, &hooks) != nil {
		return false
	}
	return len(hooks.Modules) > 0
}

// Fingerprint names a plugin's files by path, size and modification time, skipping
// .git and node_modules. It changes when a file under root does.
func Fingerprint(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	h := sha256.New()
	count := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if count++; count > fingerprintLimit {
			fmt.Fprint(h, "truncated\x00")
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// ToolHandlers keeps the hooks that can see a tool call: tool.call, tool.check, and
// the wildcards that match them.
func ToolHandlers(hooks []string) []string {
	var out []string
	for _, hook := range hooks {
		event := hook
		if i := strings.IndexByte(hook, '{'); i >= 0 {
			event = hook[:i]
		}
		switch event {
		case "*", "tool.*", "tool.call", "tool.check":
			out = append(out, hook)
		}
	}
	return out
}

// ParseValidation reads the report `claude plugin validate <dir> --json` prints and
// returns every hook the validator listed, as `event` or `event{filter}`. An error
// names a report the validator refused or one Steer cannot read.
func ParseValidation(report []byte) ([]string, error) {
	var r struct {
		Success  *bool `json:"success"`
		Manifest struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"manifest"`
		Contents []struct {
			Type   string `json:"type"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
			Notes []string `json:"notes"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(report, &r); err != nil {
		return nil, fmt.Errorf("unreadable validator report: %w", err)
	}
	var problems []string
	for _, e := range r.Manifest.Errors {
		problems = append(problems, e.Message)
	}
	hooks := []string{}
	sawHooks := false
	for _, c := range r.Contents {
		if c.Type != "hooks" {
			continue
		}
		sawHooks = true
		for _, e := range c.Errors {
			problems = append(problems, e.Message)
		}
		for _, note := range c.Notes {
			if _, list, ok := strings.Cut(note, " hooks: "); ok {
				hooks = append(hooks, splitTopLevel(list)...)
			}
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("validator refused the plugin: %s", strings.Join(problems, "; "))
	}
	if r.Success != nil && !*r.Success {
		return nil, fmt.Errorf("validator reported failure with no error message")
	}
	if !sawHooks {
		return nil, fmt.Errorf("validator report lists no hooks module")
	}
	return hooks, nil
}

// splitTopLevel splits a validator hook list at the commas outside a filter, whose
// array or pattern can hold commas of its own.
func splitTopLevel(list string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range list {
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				if s := strings.TrimSpace(list[start:i]); s != "" {
					out = append(out, s)
				}
				start = i + 1
			}
		}
	}
	if s := strings.TrimSpace(list[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// Validator runs Claude Code's validator on one plugin directory and returns its
// JSON report.
type Validator func(ctx context.Context, dir string) ([]byte, error)

// Inspect reads every candidate through validate. claudeVersion is recorded with
// each inspection for the reader; it does not decide staleness.
func Inspect(ctx context.Context, candidates []Candidate, validate Validator, claudeVersion string) []Inspection {
	out := make([]Inspection, 0, len(candidates))
	for _, c := range candidates {
		in := Inspection{ID: c.ID, Path: c.Path, Fingerprint: Fingerprint(c.Path),
			Claude: claudeVersion, InspectedAt: time.Now().Unix(), Hooks: []string{}}
		report, err := validate(ctx, c.Path)
		if err == nil {
			in.Hooks, err = ParseValidation(report)
		}
		if err != nil {
			in.Hooks = []string{}
			in.Error = err.Error()
		}
		out = append(out, in)
	}
	return out
}

func cacheKey(id, path string) string { return id + "\x00" + path }

// LoadCache reads the inspections in dir, keyed by plugin id and install path. A
// missing or unreadable cache reads as empty.
func LoadCache(dir string) map[string]Inspection {
	out := map[string]Inspection{}
	data, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		return out
	}
	var rows []Inspection
	if json.Unmarshal(data, &rows) != nil {
		return out
	}
	for _, r := range rows {
		out[cacheKey(r.ID, r.Path)] = r
	}
	return out
}

// UpdateCache writes these inspections into the cache in dir. An entry for another
// install, such as a project mod another repository enables, is kept.
func UpdateCache(dir string, rows []Inspection) error {
	cache := LoadCache(dir)
	for _, r := range rows {
		cache[cacheKey(r.ID, r.Path)] = r
	}
	all := make([]Inspection, 0, len(cache))
	for _, r := range cache {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ID != all[j].ID {
			return all[i].ID < all[j].ID
		}
		return all[i].Path < all[j].Path
	})
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".inspections.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, cacheFile))
}

// Read joins the enabled candidates to the cache in cacheDir. It runs no process.
func Read(candidates []Candidate, cacheDir string) []Exposure {
	cache := LoadCache(cacheDir)
	out := []Exposure{}
	for _, c := range candidates {
		row := Exposure{ID: c.ID, Path: c.Path, Enabled: c.Enabled, State: StateUninspected}
		in, ok := cache[cacheKey(c.ID, c.Path)]
		switch {
		case !ok:
		case in.Fingerprint != Fingerprint(c.Path):
			row.State = StateStale
			row.InspectedAt = in.InspectedAt
		case in.Error != "":
			row.State = StateFailed
			row.InspectedAt = in.InspectedAt
			row.Error = in.Error
		default:
			row.InspectedAt = in.InspectedAt
			row.Handlers = ToolHandlers(in.Hooks)
			row.State = StateNoToolHooks
			if len(row.Handlers) > 0 {
				row.State = StateToolHooks
			}
		}
		out = append(out, row)
	}
	return out
}
