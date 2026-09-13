package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
}

// LoadAddendum reads the global store from disk and the project store from the
// repository's HEAD. The index and working tree are never read, so the commit under
// review cannot change the instructions its reviewer gets.
func LoadAddendum(repo string) Addendum {
	var a Addendum
	if dir, err := hookstore.Resolve(steerpath.GlobalScope); err != nil {
		a.Errors = append(a.Errors, fmt.Sprintf("review addendum: global store: %v", err))
	} else if data, err := os.ReadFile(filepath.Join(dir, hookstore.ManifestName)); err == nil {
		a.add(steerpath.GlobalScope, data)
	} else if !os.IsNotExist(err) {
		a.Errors = append(a.Errors, fmt.Sprintf("review addendum: global store: %v", err))
	}
	if data, ok := projectManifestAtHead(repo); ok {
		a.add(steerpath.ProjectScope, data)
	}
	return a
}

// projectManifestAtHead reads the project manifest from HEAD, neutral store first and
// legacy second, the order hookstore.Resolve reads them on disk. A repository with no
// HEAD, or no manifest at HEAD, has no project entries.
func projectManifestAtHead(repo string) ([]byte, bool) {
	if repo == "" {
		return nil, false
	}
	for _, dirOf := range []func(string) (string, error){steerpath.HooksDir, steerpath.LegacyHooksDir} {
		dir, err := dirOf(steerpath.ProjectScope)
		if err != nil {
			continue
		}
		spec := "HEAD:" + filepath.ToSlash(filepath.Join(dir, hookstore.ManifestName))
		if out, ok := Git(repo, gitShort, "show", spec); ok {
			return []byte(out), true
		}
	}
	return nil, false
}

func (a *Addendum) add(scope string, data []byte) {
	var manifest struct {
		Addendum map[string]struct {
			Text string `json:"text"`
		} `json:"review_addendum"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		a.Errors = append(a.Errors, fmt.Sprintf("review addendum: %s store: %v", scope, err))
		return
	}
	ids := make([]string, 0, len(manifest.Addendum))
	for id := range manifest.Addendum {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		text := strings.Join(strings.Fields(manifest.Addendum[id].Text), " ")
		if text != "" {
			a.Entries = append(a.Entries, AddendumEntry{Scope: scope, ID: id, Text: text})
		}
	}
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
