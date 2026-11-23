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
	"github.com/steer-sh/steer-cli/internal/reviewpath"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// addendumTag bounds the operator's entries in the review prompt.
const addendumTag = "review-addendum"

// reviewedRulesTag bounds the reviewed rules a commit's changed paths matched.
const reviewedRulesTag = "reviewed-rules"

// AddendumEntry is one review-tier entry, with the store it came from.
type AddendumEntry struct {
	Scope string
	ID    string
	Text  string
}

// ReviewedRule is one rule the author was given that the reviewer also checks, for a
// commit that changes a path one of Paths matches. No paths matches every commit.
type ReviewedRule struct {
	Scope string
	ID    string
	Text  string
	Paths []string
}

// Addendum is the operator's review-tier text for one repository: global entries
// first, then project entries, each sorted by id, and the reviewed rules in the same
// order. Errors are read failures that dropped entries; the gate records them and
// reviews without those entries.
type Addendum struct {
	Entries []AddendumEntry
	Rules   []ReviewedRule
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
	if err == nil {
		var rules []ReviewedRule
		if rules, err = reviewedRules(scope, data); err == nil {
			a.Rules = append(a.Rules, rules...)
		}
	}
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

// reviewedRules decodes a manifest's rules that carry a review section, sorted by id,
// with whitespace-collapsed text. A rule with no text is dropped.
func reviewedRules(scope string, data []byte) ([]ReviewedRule, error) {
	var manifest struct {
		Rules map[string]struct {
			Text   string `json:"text"`
			Review *struct {
				Paths []string `json:"paths"`
			} `json:"review"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	var out []ReviewedRule
	for id, entry := range manifest.Rules {
		text := strings.Join(strings.Fields(entry.Text), " ")
		if entry.Review == nil || text == "" {
			continue
		}
		out = append(out, ReviewedRule{Scope: scope, ID: id, Text: text, Paths: entry.Review.Paths})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// HoldsRule reports whether the addendum holds id as a reviewed rule of scope.
func (a Addendum) HoldsRule(scope, id string) bool {
	for _, r := range a.Rules {
		if r.Scope == scope && r.ID == id {
			return true
		}
	}
	return false
}

// RulesFor returns the reviewed rules a commit changing paths receives: each rule with
// no paths, and each rule with a path that matches one of them.
func (a Addendum) RulesFor(paths []string) []ReviewedRule {
	slash := SlashPaths(paths)
	var out []ReviewedRule
	for _, r := range a.Rules {
		if len(r.Paths) == 0 || reviewpath.MatchAny(r.Paths, slash) {
			out = append(out, r)
		}
	}
	return out
}

// Key is the rule's id with its scope, <scope>:<id>, which names it in the prompt,
// on a finding's RULE line and on the row; one id can be held by both scopes.
func (r ReviewedRule) Key() string { return r.Scope + ":" + r.ID }

// AppliedRuleIDs names the reviewed rules a commit changing paths receives, by Key,
// or nil when it receives none.
func (a Addendum) AppliedRuleIDs(paths []string) []string {
	var ids []string
	for _, r := range a.RulesFor(paths) {
		ids = append(ids, r.Key())
	}
	return ids
}

// RulesBlock renders the reviewed rules a commit changing paths receives, or "" when
// it receives none. Rule text is neutralised like the diff.
func (a Addendum) RulesBlock(paths []string) string {
	return renderRules(a.RulesFor(paths))
}

func renderRules(rules []ReviewedRule) string {
	if len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<" + reviewedRulesTag + ">\n")
	b.WriteString(reviewedRulesPreamble)
	for _, r := range rules {
		b.WriteString("- [" + NeutralizeTags(r.Key()) + "] " + NeutralizeTags(r.Text) + "\n")
	}
	b.WriteString("</" + reviewedRulesTag + ">\n")
	return b.String()
}

// reviewedRulesPreamble tells the reviewer how a reviewed rule changes the rules that
// follow the context. Without it, a violation that still runs correctly is dropped or
// rated NITPICK, and a NITPICK-only round does not block.
const reviewedRulesPreamble = "The operator gave the author of this commit these rules, " +
	"each as `[id] rule`. Check the diff and the commit message against each one. " +
	"A change that clearly breaks a rule is a finding even when the code runs correctly, " +
	"and even when it is a matter of structure the rules below call style: " +
	"rate it SUBSTANTIVE, or NITPICK when the change only comes close to breaking it. " +
	"Its FAILS WHEN names the rule and the changed line that breaks it. " +
	"After its FAILS WHEN line, add one more line indented four spaces: `RULE: <id>`, " +
	"with the id as it appears inside the brackets. " +
	"A finding that no rule here describes keeps the rules below unchanged.\n"

func sortedIDs(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pendingProjectAddendumNotice tells a denied commit that the working-tree project
// manifest holds review-addendum entries, or reviewed rules that reach rulePaths,
// that differ from the project entries its review used. rulePaths is reviewedPaths
// with renames split, the paths the review matched rules against. It returns "" when
// they match or either side cannot be read. When every reviewed path is a project
// manifest, the notice omits the recovery step.
func pendingProjectAddendumNotice(repo string, reviewed Addendum, reviewedPaths, rulePaths []string) string {
	if repo == "" || reviewed.projectUnread {
		return ""
	}
	data, manifest, ok := workingTreeProjectManifest(repo)
	if !ok {
		return ""
	}
	// Each comparison stands alone: a working-tree section that does not parse skips
	// only its own comparison.
	var parts []string
	if local, err := addendumTexts(data); err == nil {
		used := map[string]string{}
		for _, e := range reviewed.Entries {
			if e.Scope == steerpath.ProjectScope {
				used[e.ID] = e.Text
			}
		}
		if d := idChanges(used, local); d != "" {
			parts = append(parts, "review-addendum changes ("+d+")")
		}
	}
	if localRules, err := reviewedRules(steerpath.ProjectScope, data); err == nil {
		if d := reviewedRuleChanges(reviewed.Rules, localRules, rulePaths); d != "" {
			parts = append(parts, "reviewed-rule changes ("+d+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	notice := fmt.Sprintf("[commit-gate] Uncommitted project %s are "+
		"present in the working tree; this review used the project entries from HEAD.",
		strings.Join(parts, " and "))
	if !onlyManifests(reviewedPaths) {
		notice += fmt.Sprintf(" To apply these changes before reviewing the code, stage only "+
			"the intended changes to %s at the repository root, commit with no paths or `-a`, then restage the code.", manifest)
	}
	return notice
}

// reviewedRuleChanges compares the project rules among head with local, by text and
// paths, for each id that reaches paths on either side, in idChanges' wording.
func reviewedRuleChanges(head, local []ReviewedRule, paths []string) string {
	var project []ReviewedRule
	for _, r := range head {
		if r.Scope == steerpath.ProjectScope {
			project = append(project, r)
		}
	}
	reach := map[string]bool{}
	for _, side := range [][]ReviewedRule{project, local} {
		for _, r := range (Addendum{Rules: side}).RulesFor(paths) {
			reach[r.ID] = true
		}
	}
	shapes := func(rules []ReviewedRule) map[string]string {
		out := map[string]string{}
		for _, r := range rules {
			if reach[r.ID] {
				out[r.ID] = r.Text + "\x00" + strings.Join(r.Paths, "\x00")
			}
		}
		return out
	}
	return idChanges(shapes(project), shapes(local))
}

// idChanges names the ids local adds to, changes from, and removes from used, as
// `added: "a"; changed: "b"; removed: "c"`, or "" when the two maps are equal.
func idChanges(used, local map[string]string) string {
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
	return strings.Join(parts, "; ")
}

// workingTreeProjectManifest reads the project manifest at the work-tree root of the
// repository holding repo, neutral store first, falling back to legacy only when the
// neutral file does not exist. No manifest in either store reads as an empty manifest;
// a root git cannot resolve, or any other read failure, is not ok. The returned path
// is root-relative.
func workingTreeProjectManifest(repo string) ([]byte, string, bool) {
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
		return data, path, true
	}
	if len(paths) == 0 {
		return nil, "", false
	}
	return []byte("{}"), paths[0], true
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

// Digest is the first 8 hex chars of sha256 over Block and every configured reviewed
// rule, or "" when there are neither. It is the instrument's addendum half. It covers
// the whole configured set, not the rules one commit's paths matched, so consecutive
// commits under one configuration share one instrument.
func (a Addendum) Digest() string {
	block := a.Block()
	if len(a.Rules) > 0 {
		block += configuredRules(a.Rules)
	}
	if block == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(block))
	return hex.EncodeToString(sum[:])[:8]
}

// configuredRules renders every reviewed rule with its scope and paths, for the digest.
func configuredRules(rules []ReviewedRule) string {
	var b strings.Builder
	b.WriteString(renderRules(rules))
	for _, r := range rules {
		b.WriteString(r.Scope + ":" + r.ID + " " + strings.Join(r.Paths, ",") + "\n")
	}
	return b.String()
}
