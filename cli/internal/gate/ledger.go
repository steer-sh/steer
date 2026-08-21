package gate

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Row is one adjudication-ledger entry. It is a map rather than a struct because the
// schema is append-only across two implementations and mine.py reads it by key: a
// typed row would have to model every field any writer has ever emitted, and an
// unmodeled key would be dropped on the floor.
type Row map[string]any

// Channel names the adapter that wrote a row, so a reader can tell a field that is
// absent by construction from one that was lost.
const (
	ChannelPreToolUse = "pretooluse"
	ChannelPreCommit  = "precommit"
)

var slugRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func slug(s string, n int) string {
	out := slugRe.ReplaceAllString(s, "")
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Digest is the 16-hex-char content address used for diffs, tasks and prefixes.
func Digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

// Turn is one human prompt from the session transcript, with its timestamp. A
// session commits more than once, so a reader takes the turns between the previous
// chain's end and this row to get the task for this diff; an undated list cannot be
// cut that way.
type Turn struct {
	TS   *float64 `json:"ts"`
	Text string   `json:"text"`
}

// Ledger owns the state directory: the JSONL shard and the three sidecar trees.
type Ledger struct {
	cfg Config
	// errs collects sidecar failures. They happen before the row is written, so they
	// ride that same row as gate_errors rather than being swallowed.
	errs []string
}

func NewLedger(cfg Config) *Ledger { return &Ledger{cfg: cfg} }

func (l *Ledger) noteErr(format string, args ...any) {
	l.errs = append(l.errs, fmt.Sprintf(format, args...))
}

// Errors returns the sidecar failures collected since the last Log.
func (l *Ledger) Errors() []string { return l.errs }

// Log appends one row to the shard. It returns an error only for the append itself:
// the caller reports that on the operator channel, which needs no disk. Every other
// failure already rode the row as gate_errors.
//
// The gate is an ephemeral process, so an error cannot be carried to a later
// invocation — the carry would need the disk that just failed.
func (l *Ledger) Log(row Row) error {
	setDefault(row, "instrument", l.cfg.InstrumentID())
	setDefault(row, "host", l.cfg.HostID)
	if len(l.errs) > 0 {
		setDefault(row, "gate_errors", l.errs)
		l.errs = nil
	}
	if err := os.MkdirAll(l.cfg.StateDir, 0o755); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode row: %w", err)
	}
	f, err := os.OpenFile(l.cfg.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append ledger: %w", err)
	}
	return nil
}

func setDefault(row Row, key string, value any) {
	if _, ok := row[key]; !ok {
		row[key] = value
	}
}

// SaveDiff writes the diff this round reviewed beside the log and returns the fields
// naming it. A row without them is readable exactly as before.
func (l *Ledger) SaveDiff(session string, firing int, diff string) Row {
	name := fmt.Sprintf("%d-%s-r%d.diff", time.Now().Unix(), slugOrDefault(session, 32), firing)
	if err := writeFile(l.cfg.DiffDir(), name, []byte(diff)); err != nil {
		l.noteErr("save_diff: %v", err)
		return Row{}
	}
	return Row{"diff_file": name, "diff_sha": Digest(diff), "diff_chars": len(diff)}
}

// SaveTask writes the session's human turns beside the log, one file per session.
// Rewritten on every row, so the file always holds the turns up to it.
func (l *Ledger) SaveTask(session string, turns []Turn) Row {
	var sb strings.Builder
	for _, turn := range turns {
		line, err := json.Marshal(turn)
		if err != nil {
			l.noteErr("save_task: encode: %v", err)
			return Row{}
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	name := slugOrDefault(session, 32) + ".jsonl"
	if err := writeFile(l.cfg.TaskDir(), name, []byte(sb.String())); err != nil {
		l.noteErr("save_task: %v", err)
		return Row{}
	}
	return Row{"task_file": name, "task_sha": Digest(sb.String())}
}

// SavePrefix stores the session's transcript as it stood when the gate fired.
//
// Copied rather than referenced, because the transcript expires and keeps growing.
// Named by content, so the row's prefix_sha always describes the file the row names.
func (l *Ledger) SavePrefix(transcript string, lines []string) Row {
	if len(lines) == 0 {
		return Row{}
	}
	text := strings.Join(lines, "\n") + "\n"
	sha := Digest(text)
	stem := slugOrDefault(Stem(transcript), 64)
	if stem == "" {
		stem = "nosession"
	}
	name := fmt.Sprintf("%s-%s.jsonl.gz", stem, sha)
	path := filepath.Join(l.cfg.PrefixDir(), name)
	if _, err := os.Stat(path); err != nil {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, 6)
		if _, err := zw.Write([]byte(text)); err != nil {
			l.noteErr("save_prefix: compress: %v", err)
			return Row{}
		}
		if err := zw.Close(); err != nil {
			l.noteErr("save_prefix: compress: %v", err)
			return Row{}
		}
		if err := writeFile(l.cfg.PrefixDir(), name, buf.Bytes()); err != nil {
			l.noteErr("save_prefix: %v", err)
			return Row{}
		}
	}
	return Row{"prefix_file": name, "prefix_lines": len(lines), "prefix_sha": sha}
}

func slugOrDefault(s string, n int) string {
	if out := slug(s, n); out != "" {
		return out
	}
	return "nosession"
}

func writeFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o644)
}
