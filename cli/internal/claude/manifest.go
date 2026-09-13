package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	SourceManual    = "manual"
	SourceGenerated = "generated"
	SourceFactory   = "factory"

	DefaultPromptVersion = "unknown"
	DefaultHookVersion   = "manual"
)

var validSources = map[string]struct{}{
	SourceManual:    {},
	SourceGenerated: {},
	SourceFactory:   {},
}

// SelfTestSummary stores high-level generated hook test outcomes.
type SelfTestSummary struct {
	Passed       bool `json:"passed"`
	PositivePass int  `json:"positive_pass"`
	PositiveFail int  `json:"positive_fail"`
	NegativePass int  `json:"negative_pass"`
	NegativeFail int  `json:"negative_fail"`
}

// FixtureBinding records what a guard's stored fixtures were verified against.
//
// The binding is four values, and an edit to any one of them drops Tested. The
// guard's own digest is not enough: rule_hash names the prose, which a code edit
// leaves untouched, so a rule reworded around unchanged code changes what the
// guard is meant to do while the stored cases may no longer cover it. The master
// digest is in for the mirror reason — a master rebuilt around an untouched
// guard can move that guard's behaviour, so a guard cannot be reported as tested
// under a master it never ran beneath.
type FixtureBinding struct {
	Digest       string `json:"digest"`
	Cases        int    `json:"cases"`
	Tested       bool   `json:"tested"`
	HookDigest   string `json:"hook_digest,omitempty"`
	MasterDigest string `json:"master_digest,omitempty"`
	RuleHash     string `json:"rule_hash,omitempty"`
	VerifiedAt   string `json:"verified_at,omitempty"`
	Shadowed     int    `json:"shadowed,omitempty"`
}

// Bound reports whether this state was earned under the identity given. It is
// never inherited: an unbound state reads as untested, never as current.
func (b *FixtureBinding) Bound(fixturesDigest, hookDigest, ruleHash, masterDigest string) bool {
	if b == nil || !b.Tested {
		return false
	}
	return b.Digest == fixturesDigest &&
		b.HookDigest == hookDigest &&
		b.RuleHash == ruleHash &&
		b.MasterDigest == masterDigest
}

// RuleEntry is one instruction-tier rule: text Steer owns and writes into every
// target's instruction document, with no guard behind it. Text is the pre-image
// RuleHash is taken over, so a reader recomputes the digest from the store rather
// than trusting a stamp made elsewhere.
type RuleEntry struct {
	Text     string `json:"text"`
	RuleHash string `json:"rule_hash"`
	Created  string `json:"created"`
	Updated  string `json:"updated,omitempty"`
}

// Manifest stores hook metadata used for list/remove/CLAUDE.md cleanup, and the
// instruction rules that carry no hook. Both are sources for the derived rules
// section, so they share one file: two files give SetRules two sources that can
// disagree after a partial write.
type Manifest struct {
	Hooks    map[string]ManifestEntry `json:"hooks"`
	Rules    map[string]RuleEntry     `json:"rules,omitempty"`
	Addendum map[string]AddendumEntry `json:"review_addendum,omitempty"`
}

// AddendumEntry is one review-tier entry: text the commit gate appends to its
// stock review prompt. TextHash is HashRule over Text.
type AddendumEntry struct {
	Text     string `json:"text"`
	TextHash string `json:"text_hash"`
	Created  string `json:"created"`
	Updated  string `json:"updated,omitempty"`
}

// ManifestEntry is metadata for one registered hook.
type ManifestEntry struct {
	File          string           `json:"file"`
	Rule          string           `json:"rule"`
	Created       string           `json:"created"`
	Updated       string           `json:"updated,omitempty"`
	Source        string           `json:"source,omitempty"`
	SourceID      string           `json:"source_id,omitempty"`
	SourceVersion string           `json:"source_version,omitempty"`
	PromptVersion string           `json:"prompt_version,omitempty"`
	PromptModules []string         `json:"prompt_modules,omitempty"`
	HookVersion   string           `json:"hook_version,omitempty"`
	Model         string           `json:"model,omitempty"`
	SkillVersion  string           `json:"skill_version,omitempty"`
	RuleHash      string           `json:"rule_hash,omitempty"`
	SelfTest      *SelfTestSummary `json:"self_test,omitempty"`
	Fixtures      *FixtureBinding  `json:"fixtures,omitempty"`
}

// ManifestListEntry is a flattened view for list output.
type ManifestListEntry struct {
	Name          string
	File          string
	Rule          string
	Created       string
	Updated       string
	Source        string
	PromptVersion string
	HookVersion   string
	Model         string
}

// NormalizeSource validates source values and applies defaults.
func NormalizeSource(source string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(source))
	if normalized == "" {
		normalized = SourceManual
	}
	if _, ok := validSources[normalized]; !ok {
		return "", fmt.Errorf("invalid source %q: expected manual, generated, or factory", source)
	}
	return normalized, nil
}

// NormalizeRule normalizes rule text for stable hashing.
func NormalizeRule(rule string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(rule)), " ")
}

// HashRule returns a stable sha256 hash of the normalized rule.
func HashRule(rule string) string {
	sum := sha256.Sum256([]byte(NormalizeRule(rule)))
	return hex.EncodeToString(sum[:])
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadManifest reads steer-manifest.json for the provided scope.
func LoadManifest(scope string) (*Manifest, error) {
	manifestPath, err := GetManifestFile(scope)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{Hooks: map[string]ManifestEntry{}}
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return manifest, nil
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return manifest, nil
	}

	if err := json.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}
	if manifest.Hooks == nil {
		manifest.Hooks = map[string]ManifestEntry{}
	}

	return manifest, nil
}

// SaveManifest writes steer-manifest.json for the provided scope.
func SaveManifest(scope string, manifest *Manifest) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	manifestPath, err := GetManifestFile(scope)
	if err != nil {
		return err
	}

	if manifest.Hooks == nil {
		manifest.Hooks = map[string]ManifestEntry{}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// AddOrUpdateHook writes one manifest entry and optionally overwrites.
func AddOrUpdateHook(scope, hookName string, entry ManifestEntry, overwrite bool) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}

	hookName = strings.TrimSpace(hookName)
	if hookName == "" {
		return fmt.Errorf("hook name cannot be empty")
	}
	existingEntry, exists := manifest.Hooks[hookName]
	if exists && !overwrite {
		return fmt.Errorf("hook already registered: %s", hookName)
	}

	now := time.Now().Format("2006-01-02")
	if entry.Created == "" {
		entry.Created = existingEntry.Created
	}
	if entry.Created == "" {
		entry.Created = now
	}
	if entry.Updated == "" {
		entry.Updated = now
	}
	if entry.File == "" {
		entry.File = existingEntry.File
	}
	entry.File = strings.TrimSpace(entry.File)
	entry.Rule = strings.TrimSpace(entry.Rule)

	source, err := NormalizeSource(entry.Source)
	if err != nil {
		return err
	}
	entry.Source = source

	if strings.TrimSpace(entry.PromptVersion) == "" {
		entry.PromptVersion = DefaultPromptVersion
	}
	if strings.TrimSpace(entry.HookVersion) == "" {
		if entry.Source == SourceGenerated {
			entry.HookVersion = entry.PromptVersion
		} else {
			entry.HookVersion = DefaultHookVersion
		}
	}
	entry.SourceID = strings.TrimSpace(entry.SourceID)
	entry.SourceVersion = strings.TrimSpace(entry.SourceVersion)
	entry.PromptModules = normalizeStringList(entry.PromptModules)
	entry.Model = strings.TrimSpace(entry.Model)
	entry.SkillVersion = strings.TrimSpace(entry.SkillVersion)
	if entry.Rule != "" {
		entry.RuleHash = HashRule(entry.Rule)
	}

	manifest.Hooks[hookName] = entry
	return SaveManifest(scope, manifest)
}

// AddHook adds a hook mapping to the manifest.
// Kept for backwards compatibility; new callers should use AddOrUpdateHook.
func AddHook(scope, hookName, fileName, rule string) error {
	return AddOrUpdateHook(scope, hookName, ManifestEntry{
		File:          fileName,
		Rule:          strings.TrimSpace(rule),
		Source:        SourceManual,
		PromptVersion: DefaultPromptVersion,
		HookVersion:   DefaultHookVersion,
	}, false)
}

// GetHookEntry returns manifest metadata for a hook name.
func GetHookEntry(scope, hookName string) (ManifestEntry, bool, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return ManifestEntry{}, false, err
	}
	entry, ok := manifest.Hooks[strings.TrimSpace(hookName)]
	return entry, ok, nil
}

// SetHookEntry upserts manifest metadata for a hook name.
func SetHookEntry(scope, hookName string, entry ManifestEntry) error {
	return AddOrUpdateHook(scope, hookName, entry, true)
}

// TouchHookUpdated refreshes the Updated date for an existing hook.
func TouchHookUpdated(scope, hookName string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	entry, ok := manifest.Hooks[strings.TrimSpace(hookName)]
	if !ok {
		return nil
	}
	entry.Updated = time.Now().Format("2006-01-02")
	manifest.Hooks[strings.TrimSpace(hookName)] = entry

	return SaveManifest(scope, manifest)
}

// RemoveHook removes a hook mapping from the manifest.
func RemoveHook(scope, hookName string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}

	if manifest.Hooks == nil {
		manifest.Hooks = map[string]ManifestEntry{}
	}

	delete(manifest.Hooks, strings.TrimSpace(hookName))
	return SaveManifest(scope, manifest)
}

// SetRuleEntry writes one instruction rule into the manifest, creating the map
// when the store holds no rule yet.
func SetRuleEntry(scope, ruleID string, entry RuleEntry) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	if manifest.Rules == nil {
		manifest.Rules = map[string]RuleEntry{}
	}
	manifest.Rules[strings.TrimSpace(ruleID)] = entry
	return SaveManifest(scope, manifest)
}

// RemoveRule deletes one instruction rule from the manifest. An empty map is
// dropped, so a store that held a rule and no longer does writes the bytes it
// wrote before any rule existed.
func RemoveRule(scope, ruleID string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	delete(manifest.Rules, strings.TrimSpace(ruleID))
	if len(manifest.Rules) == 0 {
		manifest.Rules = nil
	}
	return SaveManifest(scope, manifest)
}

// SetAddendumEntry writes one review addendum entry into the manifest.
func SetAddendumEntry(scope, id string, entry AddendumEntry) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	if manifest.Addendum == nil {
		manifest.Addendum = map[string]AddendumEntry{}
	}
	manifest.Addendum[strings.TrimSpace(id)] = entry
	return SaveManifest(scope, manifest)
}

// RemoveAddendumEntry deletes one review addendum entry. An empty map is dropped,
// as RemoveRule drops an empty rules map.
func RemoveAddendumEntry(scope, id string) error {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return err
	}
	delete(manifest.Addendum, strings.TrimSpace(id))
	if len(manifest.Addendum) == 0 {
		manifest.Addendum = nil
	}
	return SaveManifest(scope, manifest)
}

// GetRule returns the rule text for a hook name, or empty string when absent.
func GetRule(scope, hookName string) (string, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return "", err
	}

	entry, ok := manifest.Hooks[strings.TrimSpace(hookName)]
	if !ok {
		return "", nil
	}
	return entry.Rule, nil
}

// ListHooks returns all hooks from manifest in sorted order.
func ListHooks(scope string) ([]ManifestListEntry, error) {
	manifest, err := LoadManifest(scope)
	if err != nil {
		return nil, err
	}

	entries := make([]ManifestListEntry, 0, len(manifest.Hooks))
	for name, entry := range manifest.Hooks {
		entries = append(entries, ManifestListEntry{
			Name:          name,
			File:          entry.File,
			Rule:          entry.Rule,
			Created:       entry.Created,
			Updated:       entry.Updated,
			Source:        entry.Source,
			PromptVersion: entry.PromptVersion,
			HookVersion:   entry.HookVersion,
			Model:         entry.Model,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	return entries, nil
}
