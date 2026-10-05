package gate

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	setDefault(row, "instrument_fields", l.cfg.InstrumentFields())
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
	// A shard cut off mid-row would swallow this row into the fragment.
	if !endsInNewline(l.cfg.LogFile()) {
		line = append([]byte{'\n'}, line...)
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

// endsInNewline reports that path is absent, empty, unreadable, or ends in a newline.
func endsInNewline(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return true
	}
	return last[0] == '\n'
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

// prefixLockStale is how old a session file's lock must be before a writer takes it over.
const prefixLockStale = 60 * time.Second

// prefixContentName matches a content-addressed prefix file and captures its stem.
var prefixContentName = regexp.MustCompile(`^(.+)-[0-9a-f]{16}\.jsonl\.gz$`)

// SavePrefix stores the session's transcript as it stood when the gate fired.
//
// Copied rather than referenced, because the transcript expires and keeps growing.
// One file per session holds the longest prefix seen, each extension appended as a
// further gzip member; the row's prefix_lines and prefix_sha name its slice. A
// transcript that does not extend that file, or a file another gate holds, is
// written to its own content-named file instead. ReadPrefix reads either form.
func (l *Ledger) SavePrefix(transcript string, lines []string) Row {
	if len(lines) == 0 {
		return Row{}
	}
	text := strings.Join(lines, "\n") + "\n"
	sha := Digest(text)
	stem := slugOrDefault(Stem(transcript), 64)
	row := func(name string) Row {
		return Row{"prefix_file": name, "prefix_lines": len(lines), "prefix_sha": sha}
	}
	dir := l.cfg.PrefixDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		l.noteErr("save_prefix: %v", err)
		return Row{}
	}
	name := stem + ".jsonl.gz"
	if l.extendSessionPrefix(filepath.Join(dir, name), text) {
		return row(name)
	}
	name = fmt.Sprintf("%s-%s.jsonl.gz", stem, sha)
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		return row(name)
	}
	member, err := gzipMember(text)
	if err == nil {
		err = writeFile(dir, name, member)
	}
	if err != nil {
		l.noteErr("save_prefix: %v", err)
		return Row{}
	}
	return row(name)
}

// extendSessionPrefix makes the session file at path hold text as a prefix, and
// reports whether it does. It never rewrites stored bytes: the new tail becomes one
// more gzip member, swapped in by rename under the file's lock.
func (l *Ledger) extendSessionPrefix(path, text string) bool {
	lock := path + ".lock"
	if !takePrefixLock(lock) {
		return false
	}
	defer os.Remove(lock)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false
	}
	stored := ""
	if len(raw) > 0 {
		if stored, err = gunzipAll(raw); err != nil {
			return false
		}
	}
	if strings.HasPrefix(stored, text) {
		return true
	}
	if !strings.HasPrefix(text, stored) {
		return false
	}
	member, err := gzipMember(text[len(stored):])
	if err != nil {
		l.noteErr("save_prefix: %v", err)
		return false
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prefix-*")
	if err != nil {
		l.noteErr("save_prefix: %v", err)
		return false
	}
	_, err = tmp.Write(append(raw, member...))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		l.noteErr("save_prefix: %v", err)
		return false
	}
	return true
}

// takePrefixLock creates the lock file, taking over one left by a crashed writer. A
// held lock returns false at once: the caller has a correct path that needs none.
func takePrefixLock(lock string) bool {
	if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > prefixLockStale {
		os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// ReadPrefix returns the transcript prefix a row recorded: the first prefix_lines
// lines of its prefix_file, checked against prefix_sha. A content-named file that was
// merged into its session file is read from the session file.
func ReadPrefix(cfg Config, row Row) (string, error) {
	name, _ := row["prefix_file"].(string)
	sha, _ := row["prefix_sha"].(string)
	n, ok := rowInt(row["prefix_lines"])
	if name == "" || !ok || n <= 0 {
		return "", fmt.Errorf("row records no prefix")
	}
	raw, err := os.ReadFile(filepath.Join(cfg.PrefixDir(), name))
	if os.IsNotExist(err) {
		if m := prefixContentName.FindStringSubmatch(name); m != nil {
			raw, err = os.ReadFile(filepath.Join(cfg.PrefixDir(), m[1]+".jsonl.gz"))
		}
	}
	if err != nil {
		return "", err
	}
	stored, err := gunzipAll(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	end := 0
	for i := 0; i < n; i++ {
		k := strings.IndexByte(stored[end:], '\n')
		if k < 0 {
			return "", fmt.Errorf("%s holds fewer than %d lines", name, n)
		}
		end += k + 1
	}
	text := stored[:end]
	if sha != "" && Digest(text) != sha {
		return "", fmt.Errorf("%s: the first %d lines do not match prefix_sha", name, n)
	}
	return text, nil
}

func rowInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

func gzipMember(text string) ([]byte, error) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, 6)
	if _, err := zw.Write([]byte(text)); err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	return buf.Bytes(), nil
}

// gunzipAll decompresses every member of a gzip stream.
func gunzipAll(raw []byte) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	return string(data), err
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
