package mine

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// now is the wall clock for records that carry no explicit timestamp. A variable so a
// test can pin it.
var now = func() int64 { return time.Now().Unix() }

// stamp renders a unix timestamp as a local YYYY-MM-DD, or "?" for zero.
func stamp(ts float64) string {
	if ts == 0 {
		return "?"
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02")
}

func pct(rate float64) string { return fmt.Sprintf("%.1f%%", rate*100) }

// pyStr renders a value the way Python's str() would in an f-string.
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case *string:
		if t == nil {
			return "None"
		}
		return *t
	case json.Number:
		return t.String()
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return string(pyFloat(t))
	}
	return fmt.Sprint(v)
}

// --- decision headlines -----------------------------------------------------

func decisionHeadline(record Record) string {
	class := str(record["class"])
	if IsRejected(record) {
		status := strings.ToUpper(getString(record, "status", "rejected"))
		return fmt.Sprintf("%s %s  %s — %s", status, getString(record, "rejected_date", "?"),
			class, getString(record, "reason", ""))
	}
	date := str(record["installed_date"])
	if date == "" {
		date = getString(record, "rejected_date", "?")
	}
	return fmt.Sprintf("%s %s  %s -> %s", strings.ToUpper(getString(record, "status", "active")),
		date, class, str(record["tier"]))
}

func printDecisionHead(w io.Writer, record Record) {
	fmt.Fprintf(w, "#   %s\n", decisionHeadline(record))
	fmt.Fprintf(w, "#     id=%s  match=%s\n", str(record["id"]), pyRepr(str(record["match"])))
}

func printDecisionReopen(w io.Writer, record Record) {
	if r := getString(record, "reopen", ""); r != "" {
		fmt.Fprintf(w, "#     reopen if: %s\n", r)
	}
}

// printReopenIncidence scores a refusal's stored reopen regex against the corpus
// now. Without it a reason citing a subset count names a threshold no later
// reader can evaluate.
func printReopenIncidence(w io.Writer, record Record, chains []Chain) {
	expr := getString(record, "reopen_match", "")
	if expr == "" {
		return
	}
	pattern, err := compilePattern(expr)
	if err != nil {
		return
	}
	hits, denom, rate := Incidence(chains, pattern, RecordKindFilter(record))
	at := getInt(record, "reopen_at", 0)
	state := ""
	if at > 0 {
		state = ", below the reopen threshold"
		if hits >= at {
			state = fmt.Sprintf(", REOPEN CONDITION MET (>= %d)", at)
		}
	}
	fmt.Fprintf(w, "#     reopen subset %s now %d/%d chains (%s)%s\n",
		pyRepr(expr), hits, denom, pct(rate), state)
}

func printDecisionShared(w io.Writer, record Record, shared, total int) {
	printDecisionHead(w, record)
	fmt.Fprintf(w, "#     shares %d/%d of the candidate's chains\n", shared, total)
	printDecisionReopen(w, record)
}

func printDecisionIncidence(w io.Writer, record Record, chains []Chain) {
	printDecisionHead(w, record)
	pattern, err := compilePattern(str(record["match"]))
	if err == nil {
		hits, denom, rate := Incidence(chains, pattern, RecordKindFilter(record))
		fmt.Fprintf(w, "#     now %d/%d chains (%s)\n", hits, denom, pct(rate))
	}
	printDecisionReopen(w, record)
	printReopenIncidence(w, record, chains)
}

// --- list -------------------------------------------------------------------

// List shows the recorded interventions.
func List(w io.Writer, stateDir string, asJSON bool) error {
	records, err := Load(stateDir)
	if err != nil {
		return err
	}
	if asJSON {
		if records == nil {
			records = []Record{}
		}
		return writeJSON(w, records)
	}
	if len(records) == 0 {
		fmt.Fprintln(w, "# no interventions recorded yet")
		return nil
	}
	for _, r := range records {
		date := str(r["installed_date"])
		if date == "" {
			date = getString(r, "rejected_date", "?")
		}
		fmt.Fprintf(w, "%-28s %-10s %-8s %s  %s\n",
			str(r["id"]), getString(r, "status", "active"), str(r["tier"]), date, str(r["class"]))
	}
	return nil
}

// --- shared JSON output -----------------------------------------------------

// writeJSON emits a value as indented JSON, keeping json.Number floats verbatim. The
// reader of a --json stream decodes it, so key order and HTML escaping carry no meaning.
func writeJSON(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}

func nonNilRecords(records []Record) []Record {
	if records == nil {
		return []Record{}
	}
	return records
}

func nonNilMaps(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}
