package sleep

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/statestore"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// A sleep lets through only its own target in its own session, from its start
// until its end; a wake ends it early, and the log keeps both.
func TestASleepCoversOnlyItsTargetAndSessionUntilItEndsOrWakes(t *testing.T) {
	testhome.Set(t, t.TempDir())
	start := time.UnixMilli(1_700_000_000_000)
	if _, err := Put("sess-1", KindGuard, "no-direct-main-hook", 5*time.Minute, start); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                  string
		session, kind, target string
		at                    time.Duration
		live                  bool
	}{
		{"its guard in its session", "sess-1", KindGuard, "no-direct-main-hook", time.Minute, true},
		{"just before its end", "sess-1", KindGuard, "no-direct-main-hook", 5*time.Minute - time.Millisecond, true},
		{"at its end", "sess-1", KindGuard, "no-direct-main-hook", 5 * time.Minute, false},
		{"before its start", "sess-1", KindGuard, "no-direct-main-hook", -time.Second, false},
		{"another session", "sess-2", KindGuard, "no-direct-main-hook", time.Minute, false},
		{"another guard", "sess-1", KindGuard, "bare-python", time.Minute, false},
		{"a check of the same name", "sess-1", KindCheck, "no-direct-main-hook", time.Minute, false},
	}
	for _, c := range cases {
		if _, live := Find(c.session, c.kind, c.target, start.Add(c.at)); live != c.live {
			t.Errorf("%s: live = %v, want %v", c.name, live, c.live)
		}
	}

	if _, err := Put("sess.2", KindGuard, "bare-python", 5*time.Minute, start); err != nil {
		t.Fatal(err)
	}
	if _, err := Put("sess2", KindGuard, "bare-python", time.Minute, start); err != nil {
		t.Fatal(err)
	}
	if _, woke, _ := Remove("sess2", KindGuard, "bare-python", start.Add(time.Second)); !woke {
		t.Error("the wake of a session did not end its own sleep")
	}
	if _, live := Find("sess.2", KindGuard, "bare-python", start.Add(time.Minute)); !live {
		t.Error("a wake of another session whose ID differs only in punctuation removed its record")
	}
	if _, woke, err := Remove("sess-1", KindGuard, "no-direct-main-hook", start.Add(time.Minute)); err != nil || !woke {
		t.Fatalf("Remove = %v, %v; want a wake", woke, err)
	}
	if _, live := Find("sess-1", KindGuard, "no-direct-main-hook", start.Add(2*time.Minute)); live {
		t.Error("the guard still sleeps after its wake")
	}
	if _, woke, _ := Remove("sess-1", KindGuard, "no-direct-main-hook", start.Add(time.Minute)); woke {
		t.Error("a second wake reported a sleep it ended")
	}
	all, err := Events()
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for _, ev := range all {
		if ev.Target == "no-direct-main-hook" {
			events = append(events, ev)
		}
	}
	if len(events) != 2 || events[0].Action != ActionSleep || events[1].Action != ActionWake || events[0].By != ByUser ||
		events[0].UntilMS != start.Add(5*time.Minute).UnixMilli() {
		t.Errorf("events = %+v; want the sleep the user set, then its wake", events)
	}
}

// A record no command could have written, longer than the longest sleep or of
// another version, is no sleep; nor is a link in the record's place.
func TestARecordOutsideTheSleepContractIsNoSleep(t *testing.T) {
	testhome.Set(t, t.TempDir())
	now := time.UnixMilli(1_700_000_000_000)
	dir, err := statestore.Prepare(Component)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rec Record) {
		t.Helper()
		data, _ := json.Marshal(rec)
		path := Path(dir, rec.Session, rec.Kind, rec.Target)
		_ = os.Remove(path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := Record{Version: Version, Kind: KindCheck, Target: "/repo", Session: "s", By: ByUser,
		StartedMS: now.Add(-time.Minute).UnixMilli(), UntilMS: now.Add(time.Minute).UnixMilli()}
	write(base)
	if _, live := Find("s", KindCheck, "/repo", now); !live {
		t.Fatal("a well-formed record is not live")
	}
	long := base
	long.UntilMS = now.Add(MaxMinutes * time.Minute).UnixMilli()
	other := base
	other.Version = Version + 1
	for name, rec := range map[string]Record{"longer than the longest sleep": long, "another version": other} {
		write(rec)
		if _, live := Find("s", KindCheck, "/repo", now); live {
			t.Errorf("a record %s is live", name)
		}
	}
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	data, _ := json.Marshal(base)
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	path := Path(dir, "s", KindCheck, "/repo")
	_ = os.Remove(path)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, live := Find("s", KindCheck, "/repo", now); live {
		t.Error("a link in the record's place is live")
	}
	if _, err := Put("s", KindCheck, "/repo", (MaxMinutes+1)*time.Minute, now); err == nil {
		t.Error("Put accepted a sleep longer than the longest")
	}
}
