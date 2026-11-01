package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/guard"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/shellword"
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
	return listHooksInDir(hooksDir)
}

// ErrHookInOtherScope matches a miss whose identifier resolves in the scope the
// command did not read. A caller that adds its own hint to a miss tests for it:
// the reader's next step is the named --scope, not that hint.
var ErrHookInOtherScope = errors.New("hook is in the other scope")

// HookScopeMiss is that miss. It carries the identifier, the scope the lookup
// read and the scope holding the hook, and matches ErrHookInOtherScope.
type HookScopeMiss struct {
	Identifier string
	Read       string
	Holds      string
}

func (e *HookScopeMiss) Error() string {
	return fmt.Sprintf("%s is a %s hook; this read the %s store. Rerun with --scope %s",
		e.Identifier, e.Holds, e.Read, e.Holds)
}

func (e *HookScopeMiss) Is(target error) bool { return target == ErrHookInOtherScope }

// otherScope names the scope a hook lookup did not read.
func otherScope(scope string) string {
	if normalizeScope(scope) == GlobalScope {
		return ProjectScope
	}
	return GlobalScope
}

// ErrUnownedGuardFile matches an identifier that reaches a store file no entry
// owns while that file's stem is the id of a guard in a different file.
var ErrUnownedGuardFile = errors.New("guard file has no manifest entry")

// ErrNotAGuardFile matches a manifest entry naming a store file that is no guard,
// such as the manifest or the master, which a removal must not archive and delete.
var ErrNotAGuardFile = errors.New("manifest entry names a file that is not a guard")

// ResolveHook finds a hook by manifest key, full path, file name, stem, or
// readable name. An exact key names its entry's file, even when another file has
// that stem; if the file is gone that is an error, never the other file. A file no
// entry owns whose stem is another guard's id is refused, since that id would
// address the other guard's entry and fixtures.
//
// It reads one scope. A miss reports the scope it read, and names --scope when
// the identifier resolves in the other one.
func ResolveHook(scope, identifier string) (HookFile, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return HookFile{}, fmt.Errorf("hook identifier cannot be empty")
	}

	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return HookFile{}, err
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return HookFile{}, err
	}
	if entry, keyed := manifest.Hooks[id]; keyed {
		file, err := hookstore.EntryFileName(hookstore.ManifestName, id, entry.File)
		if err != nil {
			return HookFile{}, err
		}
		for _, hook := range hooks {
			if hook.FileName == file {
				hook.Name = id
				return hook, nil
			}
		}
		return HookFile{}, fmt.Errorf("guard %s names the file %s, which is not in the %s store", id, file, normalizeScope(scope))
	}
	for i := range hooks {
		hooks[i] = ownedHook(manifest, hooks[i])
	}
	if hook, ok := matchHook(hooks, id); ok {
		if entry, keyed := manifest.Hooks[hook.Name]; keyed && fileOwner(manifest, "", hook.FileName) == "" {
			return HookFile{}, fmt.Errorf("%w: %s. Its stem is the id of the guard in %s: address that guard as %s, or move %s out of the store",
				ErrUnownedGuardFile, hook.FileName, entry.File, hook.Name, hook.FileName)
		}
		return hook, nil
	}

	scope = normalizeScope(scope)
	other := otherScope(scope)
	if otherHooks, err := ListIndividualHooks(other); err == nil {
		if otherManifest, err := LoadManifest(other); err == nil {
			if _, keyed := otherManifest.Hooks[id]; keyed {
				return HookFile{}, &HookScopeMiss{Identifier: identifier, Read: scope, Holds: other}
			}
			for i := range otherHooks {
				otherHooks[i] = ownedHook(otherManifest, otherHooks[i])
			}
		}
		if _, ok := matchHook(otherHooks, id); ok {
			return HookFile{}, &HookScopeMiss{Identifier: identifier, Read: scope, Holds: other}
		}
	}

	if len(hooks) == 0 {
		return HookFile{}, fmt.Errorf("no hooks in the %s store; --scope %s reads the other one", scope, other)
	}
	return HookFile{}, fmt.Errorf("hook not found in the %s store: %s", scope, identifier)
}

// ResolveRemoval returns the guard RemoveManagedHook acts on for identifier. An
// exact manifest key names its entry's file whether or not that file exists, since
// a removal also clears an entry whose file is gone.
func ResolveRemoval(scope, identifier string) (HookFile, error) {
	id := strings.TrimSpace(identifier)
	manifest, err := LoadManifest(scope)
	if err != nil {
		return HookFile{}, err
	}
	entry, keyed := manifest.Hooks[id]
	if !keyed {
		return ResolveHook(scope, identifier)
	}
	file, err := hookstore.EntryFileName(hookstore.ManifestName, id, entry.File)
	if err != nil {
		return HookFile{}, err
	}
	if err := refuseNonGuardFile(id, file); err != nil {
		return HookFile{}, err
	}
	dir, err := GetHooksDir(scope)
	if err != nil {
		return HookFile{}, err
	}
	return HookFile{Name: id, FileName: file, Path: filepath.Join(dir, file)}, nil
}

// refuseNonGuardFile reports an entry naming a store file listHooksInDir excludes.
func refuseNonGuardFile(id, file string) error {
	if !strings.HasSuffix(file, ".py") || file == MasterHookName {
		return fmt.Errorf("%w: guard %s names %s; edit the manifest entry", ErrNotAGuardFile, id, file)
	}
	return nil
}

// StrayStemFile returns the file named for guard id that no entry owns, when id's
// entry owns a different file. A removal of id leaves that file in the store,
// where the master still runs it. It returns "" when there is none.
func StrayStemFile(scope, id string) string {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return ""
	}
	if _, keyed := manifest.Hooks[id]; !keyed || id+".py" == MasterHookName || fileOwner(manifest, "", id+".py") != "" {
		return ""
	}
	dir, err := GetHooksDir(scope)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, id+".py")); err != nil {
		return ""
	}
	return id + ".py"
}

// ownedHook keeps a registered guard's id when its file has a different stem.
// Of several entries sharing a file, the one named for the stem wins, then the
// lowest id, so one identifier always resolves to one guard.
func ownedHook(manifest *Manifest, hook HookFile) HookFile {
	if _, ok := manifest.Hooks[hook.Name]; ok {
		if file, err := hookstore.EntryFileName("manifest", hook.Name, manifest.Hooks[hook.Name].File); err == nil && file == hook.FileName {
			return hook
		}
	}
	if owner := fileOwner(manifest, "", hook.FileName); owner != "" {
		hook.Name = owner
	}
	return hook
}

// OwnedEntry returns hook under the id of the entry that owns its file, with that
// entry. A file no entry owns keeps its stem and gets a zero entry.
func OwnedEntry(manifest *Manifest, hook HookFile) (HookFile, ManifestEntry) {
	owned := ownedHook(manifest, hook)
	entry, ok := manifest.Hooks[owned.Name]
	if !ok {
		return owned, ManifestEntry{}
	}
	if file, err := hookstore.EntryFileName(hookstore.ManifestName, owned.Name, entry.File); err != nil || file != hook.FileName {
		return owned, ManifestEntry{}
	}
	return owned, entry
}

// matchHook returns the hook an identifier names: a full path, a file name, a
// stem, or the stem with its hyphens as spaces.
func matchHook(hooks []HookFile, id string) (HookFile, bool) {
	idAbs := ""
	if abs, err := filepath.Abs(id); err == nil {
		idAbs = abs
	}

	for _, hook := range hooks {
		stem := strings.TrimSuffix(hook.FileName, ".py")
		if id == hook.Path || id == hook.FileName || id == hook.Name || id == stem {
			return hook, true
		}

		readable := strings.ReplaceAll(hook.Name, "-", " ")
		if strings.EqualFold(readable, id) || strings.EqualFold(strings.ReplaceAll(stem, "-", " "), id) {
			return hook, true
		}

		if idAbs != "" {
			hookAbs, err := filepath.Abs(hook.Path)
			if err == nil && hookAbs == idAbs {
				return hook, true
			}
		}
	}

	return HookFile{}, false
}

// ConsolidateHooks rebuilds the master steer-hooks.py from all individual hooks.
//
// It clears every guard's fixture state first. A guard's state is earned under
// one master, and this call writes a new one; the rerun that follows
// consolidation re-establishes it. Clearing before the write rather than after
// means an interrupted run leaves no state rather than a stale one.
func ConsolidateHooks(scope string) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return fmt.Errorf("failed to list hooks: %w", err)
	}
	if err := refuseUnsafeGuardNames(scope, hooks); err != nil {
		return err
	}

	if err := clearFixtureState(scope); err != nil {
		return err
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
		ruleHash := ""
		owner := ownedHook(manifest, hook).Name
		if entry, ok := manifest.Hooks[owner]; ok {
			rule = strings.TrimSpace(entry.Rule)
			origin = strings.TrimSpace(entry.Source)
			sourceID = strings.TrimSpace(entry.SourceID)
			ruleHash = strings.TrimSpace(entry.RuleHash)
		}
		meta := map[string]string{
			"hook_name": owner,
			"rule":      rule,
			"source":    origin,
			"source_id": sourceID,
			// The digest is taken over the bytes this master carries, never over
			// the file on disk: the standalone guard can change after the master
			// is built, so only the embedded source identifies what ran.
			"hook_digest": sourceDigest([]byte(source.Source)),
			// The footer names the removal command for this store's scope.
			"scope": normalizeScope(scope),
		}
		if ruleHash != "" {
			meta["rule_hash"] = ruleHash
		}
		hookMeta[hook.Name] = meta
	}

	masterContent := generateMasterHook(sources, guard.RequiredImports(), hookMeta)
	if err := publishMaster(masterPath, []byte(masterContent)); err != nil {
		return fmt.Errorf("failed to write master hook file: %w", err)
	}

	return nil
}

// refuseUnsafeGuardNames refuses a store whose guard file stem or owning manifest
// key falls outside hookstore.GuardName. The stem is written into the master's
// source and the key into the shell command its block footer prints. It runs
// before anything in the store changes.
func refuseUnsafeGuardNames(scope string, hooks []HookFile) error {
	if len(hooks) == 0 {
		return nil
	}
	manifest, err := LoadManifest(scope)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}
	for _, hook := range hooks {
		for _, name := range []string{hook.Name, ownedHook(manifest, hook).Name} {
			if !hookstore.GuardName(name) {
				return fmt.Errorf("guard %q (file %q in %s): a guard name holds only letters, digits, '_', '.' and '-', and starts with a letter or digit; remove it with `steer remove %s --scope %s`, or rename the file and its manifest entry",
					name, hook.FileName, filepath.Dir(hook.Path), shellword.Quote(hook.Name), normalizeScope(scope))
			}
		}
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

// masterBuildDigestPlaceholder is the value STEER_MASTER_BUILD_DIGEST holds
// while the master is hashed. A file cannot contain its own digest, so the
// generated text is hashed with the placeholder in place and the digest is
// substituted afterwards: the firing reports the build it belongs to without
// reopening the master's path, which a rebuild can replace between load and
// firing.
const masterBuildDigestPlaceholder = "0000000000000000"

// sourceDigest is sha256 truncated to 16 hex characters, the width
// fixture.Digest uses, so an audit row's stamp compares to a fixture receipt's
// without conversion.
func sourceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// publishMaster writes the master through a temporary file in its own directory
// and renames it into place. A guard firing loads the master by path, so a
// rebuild that wrote in place could be read half-written.
func publishMaster(path string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return err
	}
	return os.Rename(name, path)
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

	builder.WriteString("STEER_MASTER_BUILD_DIGEST = \"" + masterBuildDigestPlaceholder + "\"\n\n")

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

	rendered := builder.String()
	assignment := "STEER_MASTER_BUILD_DIGEST = \"" + masterBuildDigestPlaceholder + "\""
	digest := sourceDigest([]byte(rendered))
	return strings.Replace(rendered, assignment, "STEER_MASTER_BUILD_DIGEST = \""+digest+"\"", 1)
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
