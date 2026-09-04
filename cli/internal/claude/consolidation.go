package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/version"
)

// HookFile describes an individual (non-master) hook file on disk.
type HookFile struct {
	Name     string
	FileName string
	Path     string
}

// HookSource is one installed guard's source, carried into the master verbatim.
// A guard is loaded into its own module namespace rather than spliced into the
// master's, so nothing in it is renamed and no two guards can collide.
type HookSource struct {
	Name         string
	OriginalName string
	Source       string
}

// ListIndividualHooks returns all individual .py hooks excluding steer-hooks.py.
func ListIndividualHooks(scope string) ([]HookFile, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
		return []HookFile{}, nil
	}

	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read hooks directory: %w", err)
	}

	hooks := make([]HookFile, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".py") || name == MasterHookName {
			continue
		}

		hooks = append(hooks, HookFile{
			Name:     strings.TrimSuffix(name, ".py"),
			FileName: name,
			Path:     filepath.Join(hooksDir, name),
		})
	}

	sort.Slice(hooks, func(i, j int) bool {
		return hooks[i].Name < hooks[j].Name
	})

	return hooks, nil
}

// ResolveHook finds a hook by full path, file name, stem, or readable name.
func ResolveHook(scope, identifier string) (HookFile, error) {
	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return HookFile{}, err
	}
	if len(hooks) == 0 {
		return HookFile{}, fmt.Errorf("no hooks found")
	}

	id := strings.TrimSpace(identifier)
	if id == "" {
		return HookFile{}, fmt.Errorf("hook identifier cannot be empty")
	}

	idAbs := ""
	if abs, err := filepath.Abs(id); err == nil {
		idAbs = abs
	}

	for _, hook := range hooks {
		if id == hook.Path || id == hook.FileName || id == hook.Name {
			return hook, nil
		}

		readable := strings.ReplaceAll(hook.Name, "-", " ")
		if strings.EqualFold(readable, id) {
			return hook, nil
		}

		if idAbs != "" {
			hookAbs, err := filepath.Abs(hook.Path)
			if err == nil && hookAbs == idAbs {
				return hook, nil
			}
		}
	}

	return HookFile{}, fmt.Errorf("hook not found: %s", identifier)
}

// ConsolidateHooks rebuilds the master steer-hooks.py from all individual hooks.
func ConsolidateHooks(scope string) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return fmt.Errorf("failed to list hooks: %w", err)
	}

	masterPath, err := GetMasterHookPath(scope)
	if err != nil {
		return err
	}

	if len(hooks) == 0 {
		if err := os.Remove(masterPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove master hook: %w", err)
		}
		return nil
	}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	sources := make([]HookSource, 0, len(hooks))
	hookMeta := make(map[string]map[string]string, len(hooks))

	for _, hook := range hooks {
		source, err := readHookSource(hook.Path)
		if err != nil {
			return fmt.Errorf("failed to read hook %s: %w", hook.Path, err)
		}
		sources = append(sources, source)

		rule := ""
		origin := ""
		sourceID := ""
		if entry, ok := manifest.Hooks[hook.Name]; ok {
			rule = strings.TrimSpace(entry.Rule)
			origin = strings.TrimSpace(entry.Source)
			sourceID = strings.TrimSpace(entry.SourceID)
		}
		hookMeta[hook.Name] = map[string]string{
			"hook_name": hook.Name,
			"rule":      rule,
			"source":    origin,
			"source_id": sourceID,
		}
	}

	masterContent := generateMasterHook(sources, guard.RequiredImports(), hookMeta)
	if err := os.WriteFile(masterPath, []byte(masterContent), 0o755); err != nil {
		return fmt.Errorf("failed to write master hook file: %w", err)
	}

	return nil
}

// readHookSource reads one guard verbatim and checks it defines a top-level
// main(). That entry point is the whole compatibility contract: the master
// calls main() per virtual edit and reads the guard's stdout.
func readHookSource(filePath string) (HookSource, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return HookSource{}, err
	}

	// The source is carried into the master as a JSON string literal, and
	// json.Marshal replaces invalid UTF-8 with U+FFFD. Refuse the file rather
	// than install a silently altered guard.
	if !utf8.Valid(content) {
		return HookSource{}, fmt.Errorf("hook source is not valid UTF-8")
	}

	source := string(content)
	if !definesMain(source) {
		return HookSource{}, fmt.Errorf("main function not found")
	}

	return HookSource{
		Name:         strings.TrimSuffix(filepath.Base(filePath), ".py"),
		OriginalName: filepath.Base(filePath),
		Source:       source,
	}, nil
}

func definesMain(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if isTopLevel(line) && strings.TrimSpace(line) == "def main():" {
			return true
		}
	}
	return false
}

func isTopLevel(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return len(line) == len(strings.TrimLeft(line, " \t"))
}

func generateMasterHook(sources []HookSource, importLines []string, hookMeta map[string]map[string]string) string {
	var builder strings.Builder

	builder.WriteString(`#!/usr/bin/env python3
"""
Master steer hook - consolidated from individual hooks.
Generated automatically - do not edit manually.
"""
`)
	// The provenance line names the runtime spliced in below and the CLI that
	// spliced it, so a reader can tell a master built by this binary from one
	// left behind by an older install.
	builder.WriteString(guard.StampLine(version.Version))
	builder.WriteString("\n")

	for _, imp := range importLines {
		builder.WriteString(imp)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")

	// A JSON object with string keys/values is also a valid Python dict literal:
	// every escape JSON emits is one Python reads the same way.
	builder.WriteString("STEER_HOOK_META = ")
	builder.Write(pythonDict(hookMeta))
	builder.WriteString("\n\n")

	sourceMap := make(map[string]string, len(sources))
	for _, source := range sources {
		sourceMap[source.Name] = source.Source
	}
	builder.WriteString("STEER_HOOK_SOURCES = ")
	builder.Write(pythonDict(sourceMap))
	builder.WriteString("\n\n")

	builder.WriteString(guard.RuntimeSource())
	builder.WriteString("\n")

	builder.WriteString("def main():\n")
	builder.WriteString("    steer_run([\n")
	for _, source := range sources {
		builder.WriteString(fmt.Sprintf("        %s,  # %s\n", string(pythonDict(source.Name)), source.OriginalName))
	}
	builder.WriteString("    ])\n\n")
	builder.WriteString("if __name__ == \"__main__\":\n")
	builder.WriteString("    main()\n")

	return builder.String()
}

// pythonDict renders a value as JSON, which for strings, maps and slices is also
// Python source. An encoding failure yields an empty dict rather than a master
// that will not parse.
func pythonDict(value any) []byte {
	data, err := json.MarshalIndent(value, "", "    ")
	if err != nil {
		return []byte("{}")
	}
	return data
}
