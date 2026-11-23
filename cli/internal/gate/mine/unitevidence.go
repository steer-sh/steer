package mine

import (
	"fmt"
	"io"
	"regexp"
)

// EvidenceKey is the record field that lists the baseline units a writer counted as
// hits. Each entry names its unit by identity and locator alone and quotes no text,
// so the record outlives the transcripts and ledger rows it points at.
const EvidenceKey = "evidence"

// chainEvidence lists one entry per baseline chain the class matched: the host whose
// ledger holds it, its chain_id when a row carried one, its start, the sessions that
// wrote it, and the round and time of each matching finding.
func chainEvidence(chains []Chain, pattern *regexp.Regexp, kind string) []any {
	out := []any{}
	for _, ch := range chains {
		hits := ChainHits(ch, pattern, kind)
		if len(hits) == 0 {
			continue
		}
		signals := make([]any, 0, len(hits))
		for _, f := range hits {
			signals = append(signals, map[string]any{"round": f.Round, "ts": f.TS})
		}
		entry := map[string]any{
			"unit":     "chain",
			"host":     ch.Host,
			"start_ts": ch.StartTS,
			"signals":  signals,
		}
		if id := chainIDOf(ch); id != "" {
			entry["chain_id"] = id
		}
		if len(ch.Sessions) > 0 {
			entry["sessions"] = append([]string(nil), ch.Sessions...)
		}
		out = append(out, entry)
	}
	return out
}

// chainIDOf is the first chain_id any of the chain's rows carried, or "".
func chainIDOf(ch Chain) string {
	for _, row := range ch.Rows {
		if id := str(row["chain_id"]); id != "" {
			return id
		}
	}
	return ""
}

// sessionEvidence lists one entry per baseline session the class matched: its
// harness, session ID, the host whose snapshot shard held it, its start, and the kind,
// time and transcript path of each matching signal. A path is absent where the
// snapshot recorded no source for the signal.
func sessionEvidence(units []SessionUnit, pattern *regexp.Regexp, signalKind string) []any {
	out := []any{}
	for _, u := range units {
		hits := SessionHits(u, pattern, signalKind)
		if len(hits) == 0 {
			continue
		}
		signals := make([]any, 0, len(hits))
		for _, s := range hits {
			sig := map[string]any{"kind": s.Kind, "ts": s.TS}
			if path := signalSource(u, s); path != "" {
				sig["path"] = path
			}
			signals = append(signals, sig)
		}
		entry := map[string]any{
			"unit":       "session",
			"harness":    u.Harness,
			"session_id": u.SessionID,
			"start_ts":   u.StartTS,
			"signals":    signals,
		}
		if u.Host != "" {
			entry["host"] = u.Host
		}
		out = append(out, entry)
	}
	return out
}

// signalSource is a transcript file whose contribution holds the signal, or "". A
// legacy contribution names no file, so it never answers.
func signalSource(u SessionUnit, s SessionSignal) string {
	for _, src := range u.Sources {
		if src.Path == "" {
			continue
		}
		for _, got := range src.Signals {
			if got == s {
				return src.Path
			}
		}
	}
	return ""
}

// BackfillEvidenceOpts are the inputs to BackfillEvidence.
type BackfillEvidenceOpts struct {
	Ledger   string
	Sessions []SessionUnit
	Apply    bool
}

// backfilledKey marks a record whose evidence list a backfill wrote rather than its
// writer. Such a record stays open to later backfills, which add only new entries.
const backfilledKey = "evidence_backfilled"

// evidenceIdentity is the key two entries for one unit share: a session's harness and
// ID, which the session loader keys on, or a chain's host and chain ID, or its host
// and start where its rows carried no chain ID.
func evidenceIdentity(raw any) string {
	e, _ := raw.(map[string]any)
	if str(e["unit"]) == "session" {
		return "session\x1f" + str(e["harness"]) + "\x1f" + str(e["session_id"])
	}
	if id := str(e["chain_id"]); id != "" {
		return "chain\x1f" + str(e["host"]) + "\x1f" + id
	}
	return "chain\x1f" + str(e["host"]) + "\x1f\x1f" + numString(e["start_ts"])
}

// BackfillEvidence gives each record that has a baseline and no writer-frozen
// evidence list the entries its before-window matches now, each marked "recomputed".
// A record an earlier backfill reached keeps its entries and gains only new ones.
// The frozen baseline stays as it was. It writes nothing unless Apply is set, and
// prints one line per record it reaches.
func BackfillEvidence(w io.Writer, stateDir string, o BackfillEvidenceOpts) error {
	release, err := lockInventory(stateDir)
	if err != nil {
		return err
	}
	defer release()
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	var chains []Chain
	chainsLoaded := false
	changed := 0
	for _, rec := range records {
		if _, has := rec[EvidenceKey]; has && !truthy(rec[backfilledKey]) {
			continue
		}
		if _, has := rec["baseline"]; !has {
			continue
		}
		id := str(rec["id"])
		spec, err := writableCorpus(CorpusOf(rec))
		if err != nil {
			fmt.Fprintf(w, "%s: skipped, %v\n", id, err)
			continue
		}
		scope, _, err := ParseScope(rec)
		if err != nil {
			fmt.Fprintf(w, "%s: skipped, %v\n", id, err)
			continue
		}
		pattern, err := compilePattern(str(rec["match"]))
		if err != nil {
			fmt.Fprintf(w, "%s: skipped, bad regex: %v\n", id, err)
			continue
		}
		cutoff := SplitTS(rec)
		var entries []any
		if spec.Engine == EngineSession {
			entries = sessionEvidence(baselineSessions(o.Sessions, scope, cutoff),
				pattern, SessionSignalKind(rec))
		} else {
			if !chainsLoaded {
				rows, err := LoadRows(stateDir, o.Ledger)
				if err != nil {
					return err
				}
				chains = BuildChains(rows, false)
				chainsLoaded = true
			}
			entries = chainEvidence(baselineChains(chains, scope, cutoff, float64(now())),
				pattern, RecordKindFilter(rec))
		}
		kept := asList(rec[EvidenceKey])
		seen := map[string]bool{}
		for _, e := range kept {
			seen[evidenceIdentity(e)] = true
		}
		added := 0
		for _, e := range entries {
			if seen[evidenceIdentity(e)] {
				continue
			}
			e.(map[string]any)["recomputed"] = true
			kept = append(kept, e)
			added++
		}
		base, _ := rec["baseline"].(map[string]any)
		fmt.Fprintf(w, "%s: %d unit(s) in the before-window match now, %d new; the baseline froze %d hit(s)\n",
			id, len(entries), added, numInt(base["hits"]))
		if _, has := rec[EvidenceKey]; has && added == 0 {
			continue
		}
		if kept == nil {
			kept = []any{}
		}
		rec[EvidenceKey] = kept
		rec[backfilledKey] = true
		changed++
	}
	if changed == 0 {
		fmt.Fprintln(w, "# no record gains an entry")
		return nil
	}
	if !o.Apply {
		fmt.Fprintln(w, "# preview only: rerun with --apply to write these lists")
		return nil
	}
	return save(stateDir, records)
}
