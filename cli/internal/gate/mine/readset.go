package mine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// FindingKey identifies one finding of one chain: the chain's host and start, the
// verdict and the reviewer's text.
func FindingKey(ch Chain, f Finding) string {
	sum := sha256.Sum256([]byte(ch.Host + "\x00" + pyStr(ch.StartTS) + "\x00" + f.Verdict + "\x00" + f.Text))
	return hex.EncodeToString(sum[:8])
}

// keySets maps a findings kind (accepted, repaired, deferred, rejected, both) to a set of FindingKeys.
type keySets map[string]map[string]bool

// FindingsShownPath holds the residual keys the latest whole-pile undecided dump
// printed, per kind, until `steer report mark-read` takes them as read.
func FindingsShownPath(stateDir string) string {
	return filepath.Join(stateDir, "findings_shown.json")
}

// FindingsReadPath holds the residual keys a /steer-report pass read, per kind.
func FindingsReadPath(stateDir string) string {
	return filepath.Join(stateDir, "findings_read.json")
}

// loadKeySets reads a key file. A missing or unparsable file reads as empty.
func loadKeySets(path string) keySets {
	out := keySets{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var raw map[string][]string
	if json.Unmarshal(data, &raw) != nil {
		return out
	}
	for kind, keys := range raw {
		set := map[string]bool{}
		for _, k := range keys {
			set[k] = true
		}
		out[kind] = set
	}
	return out
}

func saveKeySets(path string, sets keySets) error {
	raw := map[string][]string{}
	for kind, set := range sets {
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		raw[kind] = keys
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".keys-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// recordShown narrows the pending set of one kind to the keys every whole-pile
// dump since the last mark printed. A finding that one of those dumps did not show,
// because it arrived after that dump or a class covered it then, stays out of the
// set, so a mark by any of the passes that ran those dumps leaves it new.
func recordShown(stateDir, kind string, keys map[string]bool) error {
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	path := FindingsShownPath(stateDir)
	sets := loadKeySets(path)
	if prior, ok := sets[kind]; ok {
		both := map[string]bool{}
		for k := range keys {
			if prior[k] {
				both[k] = true
			}
		}
		keys = both
	}
	sets[kind] = keys
	return saveKeySets(path, sets)
}

// MarkFindingsRead makes each pending kind's shown set its read set and clears the
// pending file. A finding a class covered at the dump was not shown, so it leaves
// the read set and reads as new if it returns. It returns the number of keys taken
// per kind; an empty result means no dump was recorded since the last mark.
func MarkFindingsRead(stateDir string) (map[string]int, error) {
	release, err := lockInventory(stateDir)
	if err != nil {
		return nil, err
	}
	defer release()
	shown := loadKeySets(FindingsShownPath(stateDir))
	taken := map[string]int{}
	if len(shown) == 0 {
		return taken, nil
	}
	read := loadKeySets(FindingsReadPath(stateDir))
	for kind, set := range shown {
		read[kind] = set
		taken[kind] = len(set)
	}
	if err := saveKeySets(FindingsReadPath(stateDir), read); err != nil {
		return nil, err
	}
	if err := os.Remove(FindingsShownPath(stateDir)); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return taken, nil
}

// forgetRead drops every finding a retiring class's regex matches from the read
// set and from the pending set a later mark would take, so a finding the class
// stops covering reads as new. The caller holds the inventory lock.
func forgetRead(stateDir string, record Record) error {
	if CorpusOf(record) != CorpusReviewFindings || UnitOf(record) != UnitChain {
		return nil
	}
	pattern, err := compilePattern(str(record["match"]))
	if err != nil {
		return nil
	}
	paths := []string{FindingsReadPath(stateDir), FindingsShownPath(stateDir)}
	files := make([]keySets, len(paths))
	held := false
	for i, p := range paths {
		files[i] = loadKeySets(p)
		held = held || len(files[i]) > 0
	}
	if !held {
		return nil
	}
	kind := RecordKindFilter(record)
	rows, err := LoadRows(stateDir, "")
	if err != nil {
		return err
	}
	changed := make([]bool, len(paths))
	for _, ch := range BuildChains(rows, false) {
		for _, f := range ch.Findings {
			if !KindFilter(f, kind) || !pattern.MatchString(f.Text) {
				continue
			}
			key := FindingKey(ch, f)
			for i, sets := range files {
				for _, set := range sets {
					if set[key] {
						delete(set, key)
						changed[i] = true
					}
				}
			}
		}
	}
	for i, p := range paths {
		if changed[i] {
			if err := saveKeySets(p, files[i]); err != nil {
				return err
			}
		}
	}
	return nil
}
