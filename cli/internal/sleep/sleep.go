// Package sleep holds the sleeps the user sets: one guard, or one repository's
// check.sh, let through for a few minutes in one session. The guard runtime
// (cli/internal/guard/runtime.py) and the Stop runner read a sleep's record; the
// self-protection guard refuses an agent's write to this component's directory
// and an agent's steer sleep or steer wake.
package sleep

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steer-sh/steer-cli/internal/atomicfile"
	"github.com/steer-sh/steer-cli/internal/jsonl"
	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

const (
	// Component names the state directory that holds every sleep.
	Component = "sleep"
	// Version is the record format. A reader treats any other as no sleep.
	Version = 1
	// KindGuard targets a guard by name; KindCheck a repository's check.sh by identity.
	KindGuard = "guard"
	KindCheck = "check"
	// DefaultMinutes is a sleep's length when the user names none.
	DefaultMinutes = 5
	// MaxMinutes is the longest sleep. A reader treats a longer record as no sleep.
	MaxMinutes = 60
	// ByUser is who set every sleep: the commands that write one refuse an agent.
	ByUser = "user"
	// EventsName is the append-only log of each sleep and wake.
	EventsName = "sleeps.jsonl"
)

// Dir is the directory that holds every sleep. It has no legacy location.
func Dir() string { return steerpath.StateDir(Component) }

// prepare creates Dir, keeping it and the state root above it private.
func prepare() (string, error) {
	dir := Dir()
	return dir, statestore.PreparePrivate(dir)
}

// Record is one live sleep: what sleeps, in which session, from when until when.
type Record struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Session   string `json:"session"`
	By        string `json:"by"`
	StartedMS int64  `json:"started_ms"`
	UntilMS   int64  `json:"until_ms"`
}

// Event is one line of the sleep log: a sleep the user set or a wake that ended one early.
type Event struct {
	Action string `json:"action"`
	AtMS   int64  `json:"at_ms"`
	Record
}

// Event actions.
const (
	ActionSleep = "sleep"
	ActionWake  = "wake"
)

// Live reports whether the record lets its target through at now.
func (r Record) Live(now time.Time) bool {
	ms := now.UnixMilli()
	return r.Version == Version && r.StartedMS <= ms && ms < r.UntilMS &&
		r.UntilMS-r.StartedMS <= int64(MaxMinutes)*int64(time.Minute/time.Millisecond)
}

// Key names a session's directory and a target's record file: the first 8 bytes
// of its SHA-256, in hex.
func Key(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:8])
}

// Path is the record file of one target's sleep in one session under dir.
func Path(dir, session, kind, target string) string {
	return filepath.Join(dir, Key(session), kind+"-"+Key(target)+".json")
}

func validate(session, kind, target string) error {
	if kind != KindGuard && kind != KindCheck {
		return fmt.Errorf("unknown sleep kind %q", kind)
	}
	if target == "" {
		return errors.New("a sleep needs a guard name or a repository")
	}
	if session == "" {
		return errors.New("a sleep needs the session ID it applies to (--session)")
	}
	return nil
}

// Put sets a sleep of d for target in session from now, replacing any it has,
// and logs it.
func Put(session, kind, target string, d time.Duration, now time.Time) (Record, error) {
	if err := validate(session, kind, target); err != nil {
		return Record{}, err
	}
	if d < time.Minute || d > time.Duration(MaxMinutes)*time.Minute {
		return Record{}, fmt.Errorf("a sleep lasts 1 to %d minutes", MaxMinutes)
	}
	dir, err := prepare()
	if err != nil {
		return Record{}, err
	}
	rec := Record{Version: Version, Kind: kind, Target: target, Session: session, By: ByUser,
		StartedMS: now.UnixMilli(), UntilMS: now.Add(d).UnixMilli()}
	data, err := json.Marshal(rec)
	if err != nil {
		return Record{}, err
	}
	path := Path(dir, session, kind, target)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Record{}, err
	}
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return Record{}, err
	}
	// A sleep the log does not hold is withdrawn: every live sleep is on record.
	if err := appendEvent(dir, Event{Action: ActionSleep, AtMS: rec.StartedMS, Record: rec}); err != nil {
		_ = os.Remove(path)
		return Record{}, err
	}
	return rec, nil
}

// Remove ends target's sleep in session and logs the wake. It reports false when
// no live sleep was there; an expired record is removed without a log line, and a
// record of another session or target in the same file is left alone.
func Remove(session, kind, target string, now time.Time) (Record, bool, error) {
	if err := validate(session, kind, target); err != nil {
		return Record{}, false, err
	}
	dir := Dir()
	path := Path(dir, session, kind, target)
	rec, ok := read(path, session, kind, target)
	if !ok {
		return Record{}, false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Record{}, false, err
	}
	if !rec.Live(now) {
		return Record{}, false, nil
	}
	return rec, true, appendEvent(dir, Event{Action: ActionWake, AtMS: now.UnixMilli(), Record: rec})
}

// Find returns target's live sleep in session at now.
func Find(session, kind, target string, now time.Time) (Record, bool) {
	if validate(session, kind, target) != nil {
		return Record{}, false
	}
	rec, ok := read(Path(Dir(), session, kind, target), session, kind, target)
	if !ok || !rec.Live(now) {
		return Record{}, false
	}
	return rec, true
}

// read decodes a record file, refusing a link and a record for another target.
func read(path, session, kind, target string) (Record, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Record{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if json.Unmarshal(data, &rec) != nil || rec.Kind != kind || rec.Target != target || rec.Session != session {
		return Record{}, false
	}
	return rec, true
}

func appendEvent(dir string, ev Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, EventsName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(jsonl.Separated(path, append(line, '\n'))); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Events reads the sleep log, oldest first. A missing log holds none.
func Events() ([]Event, error) {
	data, err := os.ReadFile(filepath.Join(Dir(), EventsName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, line := range bytes.Split(data, []byte("\n")) {
		var ev Event
		if json.Unmarshal(line, &ev) == nil && (ev.Action == ActionSleep || ev.Action == ActionWake) {
			out = append(out, ev)
		}
	}
	return out, nil
}
