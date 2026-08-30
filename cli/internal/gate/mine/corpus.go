// Package mine is the reader half of the commit-review gate: it segments the
// adjudication ledger into chains, counts a class's chain-level incidence, and
// scores the interventions that incidence motivates. A port of
// ~/.claude/hooks/commit-review/mine.py with byte-identical output.
package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// chainOpeningPhases open a new chain when firing is back at 1. NonReviewPhases,
// which open none and join none, come from the engine (gate.NonReviewPhases).
var chainOpeningPhases = map[string]bool{"deny": true, "clean": true}

var shardNameRe = regexp.MustCompile(`^adjudication\.(.+)\.jsonl$`)

// ShardHost is the machine a shard belongs to, for rows written before the host
// stamp. The legacy unsharded adjudication.jsonl yields "unknown".
func ShardHost(name string) string {
	m := shardNameRe.FindStringSubmatch(filepath.Base(name))
	if m == nil {
		return "unknown"
	}
	return m[1]
}

// LoadRows reads every ledger shard under stateDir, or the one named file when
// ledger is non-empty. A row missing `host` takes its shard's name, so a synced
// shard of pre-stamp rows still segments correctly.
func LoadRows(stateDir, ledger string) ([]gate.Row, error) {
	var paths []string
	if ledger != "" {
		paths = []string{ledger}
	} else {
		matches, err := filepath.Glob(filepath.Join(stateDir, "adjudication*.jsonl"))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		paths = matches
	}
	var rows []gate.Row
	for _, p := range paths {
		data, err := readFileIfExists(p)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue
		}
		fallback := ShardHost(p)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			row := decodeRow(line)
			if row == nil {
				continue
			}
			if _, ok := row["host"]; !ok {
				row["host"] = fallback
			}
			recoverChangedPaths(stateDir, row)
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rowTS(rows[i]) < rowTS(rows[j])
	})
	return rows, nil
}

// decodeRow parses one ledger line, keeping numbers as json.Number so any value
// echoed into an output preserves its original text. A malformed line is skipped.
func decodeRow(line string) gate.Row {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil
	}
	return gate.Row(m)
}

func rowTS(row gate.Row) float64 { return numFloat(row["ts"]) }

// recoverChangedPaths fills a row's changed_paths from its saved diff sidecar when the
// row predates the stamp. It stamps only when it recovers at least one path: a stat-only
// sidecar (an over-budget diff) carries no file headers, and stamping its empty result
// would mark paths known-and-empty, wrongly excluding a chain that did touch files. An
// unrecoverable sidecar leaves the key absent, which a scope reads as Unknown.
func recoverChangedPaths(stateDir string, row gate.Row) {
	if _, ok := row["changed_paths"]; ok {
		return
	}
	name := str(row["diff_file"])
	if name == "" || stateDir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "diffs", name))
	if err != nil {
		return
	}
	paths := pathsFromDiff(string(data))
	if len(paths) == 0 {
		return
	}
	list := make([]any, len(paths))
	for i, p := range paths {
		list[i] = p
	}
	row["changed_paths"] = list
}

// Chain is one commit attempt: the run of rows from one firing-1 opener to the next.
type Chain struct {
	Host        string
	StartTS     any
	EndTS       any
	Rows        []gate.Row
	Findings    []Finding
	Terminal    *string
	Sessions    []string
	Instruments []string
	Files       int
	Rounds      int

	// Repo is the repository root basename a scope's repo predicate matches; RepoKnown
	// is false when no row carried repo metadata. Paths is the union of changed paths
	// across the chain's rounds; PathsKnown is false when no row stamped or recovered a
	// path list, which a scope reads as Unknown rather than an empty match.
	// AuthorModels is the set of models that authored the chain's diffs;
	// AuthorModelKnown is false when no row carried the stamp, which a confound check
	// reads as Unknown rather than a disjoint support.
	Repo             string
	RepoKnown        bool
	Paths            []string
	PathsKnown       bool
	AuthorModels     []string
	AuthorModelKnown bool

	seen map[string]bool
}

// Finding is one adjudicated concern kept after per-chain and cross-chain dedup.
type Finding struct {
	Verdict string `json:"verdict"`
	Text    string `json:"text"`
	TS      any    `json:"ts"`
	Join    any    `json:"join"`
	Round   any    `json:"round"`
}

// BuildChains groups ledger rows into chains, segmenting per host. A chain is only
// meaningful within one machine's stream: merged shards interleave by timestamp, so a
// remote row would land inside a local chain and a remote opener would split one.
func BuildChains(rows []gate.Row, strictJoins bool) []Chain {
	hostSet := map[string]bool{}
	for _, row := range rows {
		hostSet[rowHost(row)] = true
	}
	hosts := make([]string, 0, len(hostSet))
	for h := range hostSet {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	var chains []Chain
	for _, host := range hosts {
		var hostRows []gate.Row
		for _, row := range rows {
			if rowHost(row) == host {
				hostRows = append(hostRows, row)
			}
		}
		chains = append(chains, chainsForHost(hostRows, strictJoins)...)
	}
	sort.SliceStable(chains, func(i, j int) bool {
		return numFloat(chains[i].StartTS) < numFloat(chains[j].StartTS)
	})
	return chains
}

// chainsForHost segments one machine's rows, in timestamp order. Findings are
// deduplicated by normalized text within a chain, and on (ledger_ts, text) across
// chains: the window-based ledger join stamped one round's adjudication onto sibling
// rows, and that write can straddle a firing-1 deny that splits two chains.
func chainsForHost(rows []gate.Row, strictJoins bool) []Chain {
	var chains []Chain
	current := -1
	ledgerSeen := map[string]bool{}

	for _, row := range rows {
		phase := str(row["phase"])
		if gate.NonReviewPhases[phase] {
			continue
		}
		firing := numInt(row["firing"])
		if firing == 0 {
			firing = 1
		}
		opens := chainOpeningPhases[phase] && firing == 1
		if current < 0 || opens {
			chains = append(chains, Chain{
				Host:    rowHost(row),
				StartTS: row["ts"],
				EndTS:   row["ts"],
				seen:    map[string]bool{},
			})
			current = len(chains) - 1
		}
		ch := &chains[current]
		ch.Rows = append(ch.Rows, row)
		ch.EndTS = row["ts"]
		if inst := str(row["instrument"]); inst != "" {
			ch.Instruments = appendUnique(ch.Instruments, inst)
		}
		if repo := str(row["repo"]); repo != "" && !ch.RepoKnown {
			ch.Repo = slashBase(repo)
			ch.RepoKnown = true
		}
		if model := str(row["author_model"]); model != "" {
			ch.AuthorModels = appendUnique(ch.AuthorModels, model)
			ch.AuthorModelKnown = true
		}
		if raw, ok := row["changed_paths"]; ok {
			ch.PathsKnown = true
			for _, p := range asList(raw) {
				if s := str(p); s != "" {
					ch.Paths = appendUnique(ch.Paths, s)
				}
			}
		}
		if phase == "clean" || phase == "cap" {
			p := phase
			ch.Terminal = &p
		}
		if sess := str(row["session_id"]); sess != "" {
			ch.Sessions = appendUnique(ch.Sessions, sess)
		}
		if strictJoins && str(row["ledger_join"]) != "round" {
			continue
		}
		for _, raw := range asList(row["concerns"]) {
			concern, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			text := str(concern["concern"])
			key := Norm(text)
			if key == "" {
				continue
			}
			// Register the ledger write before any skip: a finding dropped as an
			// intra-chain duplicate must still block the next chain from counting it.
			var across string
			hasAcross := truthy(row["ledger_ts"])
			if hasAcross {
				across = numString(row["ledger_ts"]) + "\x1f" + key
			}
			countedElsewhere := hasAcross && ledgerSeen[across]
			if hasAcross {
				ledgerSeen[across] = true
			}
			if countedElsewhere || ch.seen[key] {
				continue
			}
			ch.seen[key] = true
			ch.Findings = append(ch.Findings, Finding{
				Verdict: strings.ToUpper(str(concern["verdict"])),
				Text:    text,
				TS:      row["ts"],
				Join:    row["ledger_join"],
				Round:   row["ledger_round"],
			})
		}
	}

	for i := range chains {
		ch := &chains[i]
		sort.Strings(ch.Sessions)
		sort.Strings(ch.Instruments)
		sort.Strings(ch.AuthorModels)
		ch.Files = maxRowInt(ch.Rows, "files", 0)
		ch.Rounds = maxRowInt(ch.Rows, "firing", 1)
	}
	return chains
}

// normWhitespaceRe matches Python's Unicode `\s`: ASCII whitespace plus the Unicode
// space separators. RE2's own `\s` is ASCII-only, which would widen the dedup class.
var normWhitespaceRe = regexp.MustCompile(
	`[\t\n\x0b\f\r \x1c\x1d\x1e\x1f\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]+`)

// Norm collapses whitespace, trims, and lowercases — the key finding-text dedup runs on.
func Norm(text string) string {
	collapsed := normWhitespaceRe.ReplaceAllString(text, " ")
	return strings.ToLower(strings.Trim(collapsed, " "))
}

// slashBase is filepath.Base over forward slashes, so a repo path stays basenamed the
// same way on every host — git reports forward slashes, and changed_paths are stored
// slash-normalised, so the repo predicate must read the one separator too.
func slashBase(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func rowHost(row gate.Row) string {
	if h := str(row["host"]); h != "" {
		return h
	}
	return "unknown"
}

func maxRowInt(rows []gate.Row, key string, floor int) int {
	best := floor
	for _, row := range rows {
		v := numInt(row[key])
		if key == "firing" && v == 0 {
			v = 1
		}
		if v > best {
			best = v
		}
	}
	return best
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// --- value helpers over json.Number-decoded rows ----------------------------

func numFloat(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func numInt(v any) int { return int(numFloat(v)) }

func numString(v any) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case string:
		return n
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return ""
}

func truthy(v any) bool {
	switch n := v.(type) {
	case nil:
		return false
	case bool:
		return n
	case string:
		return n != ""
	case json.Number:
		f, _ := n.Float64()
		return f != 0
	case float64:
		return n != 0
	}
	return v != nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
