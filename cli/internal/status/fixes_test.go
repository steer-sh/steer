package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

func writeStore(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addendumByID(rows []AddendumInfo) map[string]AddendumInfo {
	out := map[string]AddendumInfo{}
	for _, r := range rows {
		out[r.Scope+"/"+r.ID] = r
	}
	return out
}

// A project entry is read on disk and at HEAD, so each pending change reads as its
// own state: an entry not committed yet, a text change not committed yet, and an
// entry removed on disk that the gate still reads until the removal is committed.
func TestAddendumInventoryReadsEachPendingChangeAgainstHead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	writeStore(t, filepath.Join(home, ".steer", "hooks"),
		`{"review_addendum":{"no-nits":{"text":"style nits are not findings"}}}`)
	writeStore(t, filepath.Join(".steer", "hooks"), `{"review_addendum":{
		"live":{"text":"sorted  output is fine"},
		"new":{"text":"a new entry"},
		"reworded":{"text":"the text on disk"}}}`)
	head := &gate.Addendum{Entries: []gate.AddendumEntry{
		{Scope: "global", ID: "no-nits", Text: "style nits are not findings"},
		{Scope: "project", ID: "live", Text: "sorted output is fine"},
		{Scope: "project", ID: "reworded", Text: "the committed text"},
		{Scope: "project", ID: "removed", Text: "removed on disk only"},
	}}

	rows := addendumByID(AddendumInventory(head))
	for key, want := range map[string]string{
		"global/no-nits":   AddendumLive,
		"project/live":     AddendumLive,
		"project/new":      AddendumUncommitted,
		"project/reworded": AddendumUncommittedChange,
		"project/removed":  AddendumUncommittedRemoval,
	} {
		if got := rows[key].State; got != want {
			t.Errorf("%s state = %q; want %q (rows %+v)", key, got, want, rows)
		}
	}
	if got := rows["project/removed"].HeadText; got != "removed on disk only" {
		t.Errorf("a removal pending commit carries the text the gate still reads, got %q", got)
	}
	if got := rows["project/reworded"]; got.Text != "the text on disk" || got.HeadText != "the committed text" {
		t.Errorf("a pending change carries both texts, got %+v", got)
	}
	if len(rows) != 5 {
		t.Errorf("rows = %+v; want five entries", rows)
	}
}

// A HEAD that was not read, or could not be, leaves a project entry's state
// unknown, never live; a global entry is read from disk either way.
func TestAddendumInventoryNeverReadsAnUnreadHeadAsLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	writeStore(t, filepath.Join(home, ".steer", "hooks"), `{"review_addendum":{"g":{"text":"global"}}}`)
	writeStore(t, filepath.Join(".steer", "hooks"), `{"review_addendum":{"p":{"text":"project"}}}`)

	for name, head := range map[string]*gate.Addendum{
		"unread":  nil,
		"corrupt": {Errors: []string{"review addendum: project store: bad json"}},
	} {
		rows := addendumByID(AddendumInventory(head))
		if got := rows["project/p"]; got.State != AddendumUnreadable || got.Detail == "" {
			t.Errorf("%s head: project entry = %+v; want unreadable with a reason", name, got)
		}
		if got := rows["global/g"].State; got != AddendumLive {
			t.Errorf("%s head: global entry state = %q; want live", name, got)
		}
	}
}
