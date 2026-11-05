package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// addendumTag bounds the operator's entries in the review prompt.
const addendumTag = "review-addendum"

// AddendumEntry is one review-tier entry, with the store it came from.
type AddendumEntry struct {
	Scope string
	ID    string
	Text  string
}

// Addendum is the operator's review-tier text for one repository: global entries
// first, then project entries, each sorted by id. Errors are read failures that
// dropped entries; the gate records them and reviews without those entries.
type Addendum struct {
	Entries []AddendumEntry
	Errors  []string
	// projectUnread marks a project manifest at HEAD that did not parse.
	projectUnread bool
}

// LoadAddendum reads the global store from disk and the project store from the
// repository's HEAD. The index and working tree are never read, so the commit under
// review cannot change the instructions its reviewer gets.
func LoadAddendum(repo string) Addendum {
	var a Addendum
	a.addGlobal()
	if data, ok := projectManifestAtHead(repo); ok {
		a.add(steerpath.ProjectScope, data)
	}
	return a
}

// PendingAddendum is the addendum the gate will read in repo once the project store
// on disk is committed: the global store, then the project store from the working
// tree. A repo of "" has no project entries.
func PendingAddendum(repo string) Addendum {
	var a Addendum
	a.addGlobal()
	if repo == "" {
		return a
	}
	texts, _, ok := workingTreeProjectAddendum(repo)
	if !ok {
		a.Errors = append(a.Errors, "review addendum: project store: unreadable in the working tree")
		a.projectUnread = true
		return a
	}
	for _, id := range sortedIDs(texts) {
		a.Entries = append(a.Entries, AddendumEntry{Scope: steerpath.ProjectScope, ID: id, Text: texts[id]})
	}
	return a
}

func (a *Addendum) addGlobal() {
	if dir, err := hookstore.Resolve(steerpath.GlobalScope); err != nil {
		a.Errors = append(a.Errors, fmt.Sprintf("review addendum: global store: %v", err))
	} else if data, err := os.ReadFile(filepath.Join(dir, hookstore.ManifestName)); err == nil {
		a.add(steerpath.GlobalScope, data)
	} else if !os.IsNotExist(err) {
		a.Errors = append(a.Errors, fmt.Sprintf("review addendum: global store: %v", err))
	}
}

// projectManifestAtHead reads the project manifest from HEAD, neutral store first and
// legacy second, the order hookstore.Resolve reads them on disk. A repository with no
// HEAD, or no manifest at HEAD, has no project entries.
func projectManifestAtHead(repo string) ([]byte, bool) {
	if repo == "" {
		return nil, false
	}
	for _, path := range projectManifestPaths() {
		if out, ok := Git(repo, gitShort, "show", "HEAD:"+path); ok {
			return []byte(out), true
		}
	}
	return nil, false
}

// projectManifestPaths lists the repo-relative slash paths of the project manifest,
// neutral store first.
func projectManifestPaths() []string {
	var paths []string
	for _, dir := range steerpath.ProjectHooksRel() {
		paths = append(paths, filepath.ToSlash(filepath.Join(dir, hookstore.ManifestName)))
	}
	return paths
}

func (a *Addendum) add(scope string, data []byte) {
	texts, err := addendumTexts(data)
	if err != nil {
		a.Errors = append(a.Errors, fmt.Sprintf("review addendum: %s store: %v", scope, err))
		if scope == steerpath.ProjectScope {
			a.projectUnread = true
		}
		return
	}
	for _, id := range sortedIDs(texts) {
		a.Entries = append(a.Entries, AddendumEntry{Scope: scope, ID: id, Text: texts[id]})
	}
}

// addendumTexts decodes a manifest's review_addendum as id -> whitespace-collapsed
// text, dropping entries with no text.
func addendumTexts(data []byte) (map[string]string, error) {
	var manifest struct {
		Addendum map[string]struct {
			Text string `json:"text"`
		} `json:"review_addendum"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	texts := map[string]string{}
	for id, entry := range manifest.Addendum {
		if text := strings.Join(strings.Fields(entry.Text), " "); text != "" {
			texts[id] = text
		}
	}
	return texts, nil
}

func sortedIDs(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pendingProjectAddendumNotice tells a denied commit that the working-tree project
// manifest holds addendum entries that differ from the project entries its review
// used. It returns "" when they match or either side cannot be read. When every
// reviewed path is a project manifest, the notice omits the recovery step.
func pendingProjectAddendumNotice(repo string, reviewed Addendum, reviewedPaths []string) string {
	if repo == "" || reviewed.projectUnread {
		return ""
	}
	local, manifest, ok := workingTreeProjectAddendum(repo)
	if !ok {
		return ""
	}
	used := map[string]string{}
	for _, e := range reviewed.Entries {
		if e.Scope == steerpath.ProjectScope {
			used[e.ID] = e.Text
		}
	}
	var added, changed, removed []string
	for _, id := range sortedIDs(local) {
		if text, found := used[id]; !found {
			added = append(added, fmt.Sprintf("%q", id))
		} else if text != local[id] {
			changed = append(changed, fmt.Sprintf("%q", id))
		}
	}
	for _, id := range sortedIDs(used) {
		if _, found := local[id]; !found {
			removed = append(removed, fmt.Sprintf("%q", id))
		}
	}
	var parts []string
	for _, group := range []struct {
		label string
		ids   []string
	}{{"added", added}, {"changed", changed}, {"removed", removed}} {
		if len(group.ids) > 0 {
			parts = append(parts, group.label+": "+strings.Join(group.ids, ", "))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	notice := fmt.Sprintf("[commit-gate] Uncommitted project review-addendum changes (%s) are "+
		"present in the working tree; this review used the project entries from HEAD.",
		strings.Join(parts, "; "))
	if !onlyManifests(reviewedPaths) {
		notice += fmt.Sprintf(" To apply these changes before reviewing the code, stage only "+
			"the intended changes to %s at the repository root, commit with no paths or `-a`, then restage the code.", manifest)
	}
	return notice
}

// workingTreeProjectAddendum reads the project manifest at the work-tree root of the
// repository holding repo, neutral store first, falling back to legacy only when the
// neutral file does not exist. No manifest in either store is an empty addendum; a
// root git cannot resolve, or any other read or parse failure, is not ok. The returned
// path is root-relative.
func workingTreeProjectAddendum(repo string) (map[string]string, string, bool) {
	out, ok := Git(repo, gitShort, "rev-parse", "--show-toplevel")
	root := strings.TrimSuffix(out, "\n")
	if !ok || root == "" {
		return nil, "", false
	}
	paths := projectManifestPaths()
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, "", false
		}
		texts, err := addendumTexts(data)
		if err != nil {
			return nil, "", false
		}
		return texts, path, true
	}
	if len(paths) == 0 {
		return nil, "", false
	}
	return map[string]string{}, paths[0], true
}

func onlyManifests(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	manifests := map[string]bool{}
	for _, path := range projectManifestPaths() {
		manifests[path] = true
	}
	for _, path := range SlashPaths(paths) {
		if !manifests[path] {
			return false
		}
	}
	return true
}

// Block renders the entries as the prompt section, or "" when there are none. Entry
// text is neutralised like the diff, so no entry can close the section early.
func (a Addendum) Block() string {
	if len(a.Entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<" + addendumTag + ">\n")
	b.WriteString("The operator recorded these as known false positives. " +
		"Do not raise a finding that one of them describes.\n")
	for _, e := range a.Entries {
		b.WriteString("- " + NeutralizeTags(e.Text) + "\n")
	}
	b.WriteString("</" + addendumTag + ">\n")
	return b.String()
}

// Digest is the first 8 hex chars of sha256 over Block, or "" when there are no
// entries. It is the instrument's addendum half.
func (a Addendum) Digest() string {
	block := a.Block()
	if block == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(block))
	return hex.EncodeToString(sum[:])[:8]
}
