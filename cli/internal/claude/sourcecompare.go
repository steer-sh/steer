package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The state one guard's source is in, comparing a store against a master.
const (
	// SourceMatches: the master embeds this guard's file byte for byte.
	SourceMatches = "matches"
	// SourceDiffers: the master embeds this guard under different bytes. The
	// comparison is directionless. Which artifact the operator wants running is
	// not a fact about the bytes, so no repair follows from this state alone.
	SourceDiffers = "differs"
	// SourceAbsentFromMaster: a guard in the store the master does not carry.
	SourceAbsentFromMaster = "absent-from-master"
	// SourceAbsentFromStore: a guard the master carries with no file behind it.
	SourceAbsentFromStore = "absent-from-store"
)

// The state a whole comparison is in. Complete is reserved for a comparison
// whose inputs were all read: an unreadable guard is not an absent one, and a
// store that could not be listed is not an empty one.
const (
	ComparisonComplete      = "complete"
	ComparisonNoMaster      = "no-master"
	ComparisonUnavailable   = "unavailable"
	ComparisonNotApplicable = "not-applicable"
)

// GuardSource is one guard's reading.
type GuardSource struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// SourceComparison is one store read against one master.
type SourceComparison struct {
	// StoreDir and MasterPath are the two artifacts compared.
	StoreDir   string `json:"store_dir,omitempty"`
	MasterPath string `json:"master_path,omitempty"`
	State      string `json:"state"`
	// Reason says what stopped a comparison that is not complete.
	Reason string        `json:"reason,omitempty"`
	Guards []GuardSource `json:"guards,omitempty"`
}

// Drifted names the guards whose source does not agree, sorted. It is empty on a
// comparison that reached no answer, so a caller tests Complete first.
func (c SourceComparison) Drifted() []string {
	out := []string{}
	for _, g := range c.Guards {
		if g.State != SourceMatches {
			out = append(out, g.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Complete reports whether every input was read.
func (c SourceComparison) Complete() bool { return c.State == ComparisonComplete }

// Agrees reports whether the master carries exactly the store's guards with
// their current source. An incomplete comparison never agrees.
func (c SourceComparison) Agrees() bool { return c.Complete() && len(c.Drifted()) == 0 }

// CompareMasterSources reads every guard file in storeDir against the source
// masterPath embeds for it, comparing the source itself and not its recorded
// digest.
func CompareMasterSources(storeDir, masterPath string) SourceComparison {
	out := SourceComparison{StoreDir: storeDir, MasterPath: masterPath, State: ComparisonComplete}
	if strings.TrimSpace(storeDir) == "" || strings.TrimSpace(masterPath) == "" {
		out.State = ComparisonNotApplicable
		out.Reason = "no store or no master to compare"
		return out
	}

	hooks, err := listHooksInDir(storeDir)
	if err != nil {
		out.State = ComparisonUnavailable
		out.Reason = err.Error()
		return out
	}

	data, err := os.ReadFile(masterPath)
	if os.IsNotExist(err) {
		out.State = ComparisonNoMaster
		out.Reason = fmt.Sprintf("no master at %s", masterPath)
		return out
	}
	if err != nil {
		out.State = ComparisonUnavailable
		out.Reason = err.Error()
		return out
	}
	embedded, err := embeddedSources(masterPath, string(data))
	if err != nil {
		out.State = ComparisonUnavailable
		out.Reason = err.Error()
		return out
	}

	for _, hook := range hooks {
		source, err := readHookSource(hook.Path)
		if err != nil {
			out.State = ComparisonUnavailable
			out.Reason = fmt.Sprintf("failed to read hook %s: %v", hook.Path, err)
			return out
		}
		held, ok := embedded[source.Name]
		switch {
		case !ok:
			out.Guards = append(out.Guards, GuardSource{Name: source.Name, State: SourceAbsentFromMaster})
		case held != source.Source:
			out.Guards = append(out.Guards, GuardSource{Name: source.Name, State: SourceDiffers})
		default:
			out.Guards = append(out.Guards, GuardSource{Name: source.Name, State: SourceMatches})
		}
		delete(embedded, source.Name)
	}
	for name := range embedded {
		out.Guards = append(out.Guards, GuardSource{Name: name, State: SourceAbsentFromStore})
	}
	sort.Slice(out.Guards, func(i, j int) bool { return out.Guards[i].Name < out.Guards[j].Name })
	return out
}

// embeddedSources decodes the object a generated master carries its guards in.
func embeddedSources(path, text string) (map[string]string, error) {
	start := strings.Index(text, masterSourcesPrefix)
	if start < 0 {
		return nil, fmt.Errorf("%s carries no guard sources", path)
	}
	out := map[string]string{}
	decoder := json.NewDecoder(strings.NewReader(text[start+len(masterSourcesPrefix):]))
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: unreadable guard sources: %w", path, err)
	}
	return out, nil
}

// MasterEmbedsGuard reports whether the scope's own master carries one guard's
// current source. It reads the store master, which is the master a fixture run
// replays against; the registered master is a separate fact.
func MasterEmbedsGuard(scope, hook string) (bool, error) {
	dir, err := GetHooksDir(scope)
	if err != nil {
		return false, err
	}
	path, err := GetMasterHookPath(scope)
	if err != nil {
		return false, err
	}
	c := CompareMasterSources(dir, path)
	if !c.Complete() {
		return false, fmt.Errorf("%s", c.Reason)
	}
	resolved, err := ResolveHook(scope, hook)
	if err != nil {
		return false, err
	}
	stem := strings.TrimSuffix(resolved.FileName, ".py")
	for _, g := range c.Guards {
		if g.Name == stem {
			return g.State == SourceMatches, nil
		}
	}
	return false, fmt.Errorf("%s carries no source for %s", path, hook)
}

// gapPhrase is how one disagreeing guard reads in a refusal.
func gapPhrase(g GuardSource) string {
	switch g.State {
	case SourceAbsentFromMaster:
		return g.Name + " is not in it"
	case SourceDiffers:
		return g.Name + " differs from its file"
	case SourceAbsentFromStore:
		return g.Name + " has no file in the store"
	}
	return g.Name
}

// listHooksInDir returns the individual guards in one store directory. A
// directory that does not exist holds no guards and is not a read failure.
func listHooksInDir(dir string) ([]HookFile, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return []HookFile{}, nil
	}
	entries, err := os.ReadDir(dir)
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
			Path:     filepath.Join(dir, name),
		})
	}
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].Name < hooks[j].Name })
	return hooks, nil
}
