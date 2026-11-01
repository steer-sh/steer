package gate_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func prefixCfg(t *testing.T) gate.Config {
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	return cfg
}

// roundTrip returns the row as a reader of the shard sees it.
func roundTrip(t *testing.T, row gate.Row) gate.Row {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var out gate.Row
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func prefixFiles(t *testing.T, cfg gate.Config) []string {
	t.Helper()
	entries, err := os.ReadDir(cfg.PrefixDir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func mustReadPrefix(t *testing.T, cfg gate.Config, row gate.Row, want []string) {
	t.Helper()
	got, err := gate.ReadPrefix(cfg, roundTrip(t, row))
	if err != nil {
		t.Fatalf("ReadPrefix(%v): %v", row, err)
	}
	if w := strings.Join(want, "\n") + "\n"; got != w {
		t.Fatalf("ReadPrefix(%v) = %q, want %q", row, got, w)
	}
}

func TestSavePrefixExtendsOneFilePerSession(t *testing.T) {
	cfg := prefixCfg(t)
	ledger := gate.NewLedger(cfg)
	lines := []string{`{"a":1}`, `{"b":2}`, `{"c":3}`, `{"d":4}`}
	transcript := "/x/sess-1.jsonl"

	first := ledger.SavePrefix(transcript, lines[:2])
	second := ledger.SavePrefix(transcript, lines[:4])
	older := ledger.SavePrefix(transcript, lines[:3])

	if files := prefixFiles(t, cfg); len(files) != 1 || files[0] != "sess-1.jsonl.gz" {
		t.Fatalf("one session file expected, got %v", files)
	}
	mustReadPrefix(t, cfg, first, lines[:2])
	mustReadPrefix(t, cfg, second, lines[:4])
	mustReadPrefix(t, cfg, older, lines[:3])
	if errs := ledger.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestSavePrefixDivergedTranscriptGetsOwnFile(t *testing.T) {
	cfg := prefixCfg(t)
	ledger := gate.NewLedger(cfg)
	transcript := "/x/sess-2.jsonl"

	kept := ledger.SavePrefix(transcript, []string{"a", "b"})
	diverged := ledger.SavePrefix(transcript, []string{"a", "c"})

	if diverged["prefix_file"] == kept["prefix_file"] {
		t.Fatalf("a diverged transcript must not share the session file: %v", diverged)
	}
	mustReadPrefix(t, cfg, kept, []string{"a", "b"})
	mustReadPrefix(t, cfg, diverged, []string{"a", "c"})
}

func TestSavePrefixHeldLockFallsBackToContentName(t *testing.T) {
	cfg := prefixCfg(t)
	ledger := gate.NewLedger(cfg)
	transcript := "/x/sess-3.jsonl"
	first := ledger.SavePrefix(transcript, []string{"a"})
	lock := filepath.Join(cfg.PrefixDir(), "sess-3.jsonl.gz.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	row := ledger.SavePrefix(transcript, []string{"a", "b"})

	if row["prefix_file"] == first["prefix_file"] {
		t.Fatalf("a held lock must leave the session file alone: %v", row)
	}
	mustReadPrefix(t, cfg, first, []string{"a"})
	mustReadPrefix(t, cfg, row, []string{"a", "b"})
}

func TestReadPrefixFindsMergedContentFile(t *testing.T) {
	cfg := prefixCfg(t)
	if err := os.MkdirAll(cfg.PrefixDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("a\nb\nc\n"))
	zw.Close()
	if err := os.WriteFile(filepath.Join(cfg.PrefixDir(), "sess-4.jsonl.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	row := gate.Row{
		"prefix_file":  "sess-4-" + gate.Digest("a\nb\n") + ".jsonl.gz",
		"prefix_lines": 2,
		"prefix_sha":   gate.Digest("a\nb\n"),
	}

	mustReadPrefix(t, cfg, row, []string{"a", "b"})

	row["prefix_sha"] = gate.Digest("x\n")
	if _, err := gate.ReadPrefix(cfg, row); err == nil {
		t.Fatal("a slice that does not match prefix_sha must be refused")
	}
}
