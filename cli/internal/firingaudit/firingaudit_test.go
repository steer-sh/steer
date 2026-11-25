package firingaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sidecarRow writes a bundle to the store's payload directory and returns the row
// naming it, stamped as the master would stamp it.
func sidecarRow(t *testing.T, dir, id, bundle string) Row {
	t.Helper()
	write(t, filepath.Join(dir, PayloadsDir, id+".json"), bundle)
	sum := sha256.Sum256([]byte(bundle))
	return Row{
		FiringID:         id,
		EvidenceRef:      PayloadsDir + "/" + id + ".json",
		EvidenceBytes:    len(bundle),
		EvidenceSHA256:   hex.EncodeToString(sum[:]),
		EvidenceComplete: true,
	}
}

// A bundle held outside the row is returned only when the file still matches both
// stamps the row recorded, so a reader never judges a block on a bundle it cannot
// tie to that firing.
func TestASidecarResolvesOnlyWhileItMatchesTheRowsStamps(t *testing.T) {
	dir := t.TempDir()
	bundle := `{"payload":{"tool_name":"Bash"},"edit":{}}`
	row := sidecarRow(t, dir, "aa", bundle)
	path := filepath.Join(dir, PayloadsDir, "aa.json")

	got := row.Resolve(dir)
	if got.Source != Sidecar {
		t.Fatalf("an intact sidecar: got %q (%s), want %q", got.Source, got.Reason, Sidecar)
	}
	if string(got.Bundle) != bundle {
		t.Errorf("the bundle read back: got %q, want %q", got.Bundle, bundle)
	}

	// Same length, different bytes: only the digest separates these.
	swapped := strings.Replace(bundle, "Bash", "Read", 1)
	if len(swapped) != len(bundle) {
		t.Fatalf("the tampered bundle must keep its length")
	}
	write(t, path, swapped)
	got = row.Resolve(dir)
	if got.Source != Damaged {
		t.Errorf("a sidecar edited in place: got %q, want %q", got.Source, Damaged)
	}
	if len(got.Bundle) != 0 {
		t.Errorf("a damaged sidecar must hand back no bundle, got %q", got.Bundle)
	}
	if !strings.Contains(got.Reason, "digests to") {
		t.Errorf("the reason must name the digest disagreement, got %q", got.Reason)
	}

	write(t, path, bundle+" ")
	if got = row.Resolve(dir); got.Source != Damaged {
		t.Errorf("a sidecar of the wrong length: got %q, want %q", got.Source, Damaged)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got = row.Resolve(dir); got.Source != Damaged {
		t.Errorf("an absent sidecar: got %q, want %q", got.Source, Damaged)
	}
}

// Both stamps are required. A sidecar missing either one cannot be checked against
// the row that names it, so it is not evidence, however intact the file is.
func TestASidecarMissingEitherStampIsNotEvidence(t *testing.T) {
	dir := t.TempDir()
	intact := sidecarRow(t, dir, "bb", `{"payload":{}}`)
	if got := intact.Resolve(dir); got.Source != Sidecar {
		t.Fatalf("the fixture must resolve while both stamps are present: %q", got.Source)
	}

	noDigest := intact
	noDigest.EvidenceSHA256 = ""
	if got := noDigest.Resolve(dir); got.Source != Damaged {
		t.Errorf("a sidecar with no digest: got %q, want %q", got.Source, Damaged)
	}

	noLength := intact
	noLength.EvidenceBytes = 0
	got := noLength.Resolve(dir)
	if got.Source != Damaged {
		t.Errorf("a sidecar with no length: got %q, want %q", got.Source, Damaged)
	}
	if len(got.Bundle) != 0 {
		t.Errorf("an unchecked sidecar must hand back no bundle, got %q", got.Bundle)
	}
	if !strings.Contains(got.Reason, "no length and digest") {
		t.Errorf("the reason must name the missing stamps, got %q", got.Reason)
	}
}

// The JSON null decodes to four bytes, which is a value and not a bundle. A row
// carrying it holds no evidence and must read as one that captured none.
func TestANullEvidenceFieldIsNotAnInlineBundle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, AuditLogName),
		`{"firing_id":"n1","hook_name":"g","evidence":null}`+"\n")

	rows, _ := Load(dir)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := len(rows[0].Evidence); got == 0 {
		t.Fatalf("the fixture must decode null to bytes for this test to bite, got %d", got)
	}
	got := rows[0].Resolve(dir)
	if got.Source != Unrecorded {
		t.Errorf("a null evidence field: got %q, want %q", got.Source, Unrecorded)
	}
	if len(got.Bundle) != 0 {
		t.Errorf("a null evidence field must hand back no bundle, got %q", got.Bundle)
	}
}

// A reference is resolved inside the guard store and nowhere else.
func TestAReferenceLeavingTheStoreIsRefusedRatherThanRead(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.json")
	write(t, outside, `{"payload":{}}`)
	sum := sha256.Sum256([]byte(`{"payload":{}}`))

	row := Row{
		FiringID:       "cc",
		EvidenceRef:    "../" + filepath.Base(dir) + "/outside.json",
		EvidenceBytes:  len(`{"payload":{}}`),
		EvidenceSHA256: hex.EncodeToString(sum[:]),
	}
	got := row.Resolve(filepath.Join(dir, "store"))
	if got.Source != Damaged {
		t.Errorf("a reference climbing out of the store: got %q, want %q", got.Source, Damaged)
	}
	if len(got.Bundle) != 0 {
		t.Errorf("a refused reference must read nothing, got %q", got.Bundle)
	}
}

// A row small enough to carry its own bundle needs no store to resolve, and the
// bytes handed back are the ones the master wrote.
func TestAnInlineRowResolvesWithoutTheStore(t *testing.T) {
	inline := json.RawMessage(`{"payload":{"tool_name":"Bash"},"edit":null}`)
	row := Row{FiringID: "dd", Evidence: inline, EvidenceComplete: true}

	got := row.Resolve("")
	if got.Source != Inline {
		t.Fatalf("an inline row: got %q (%s), want %q", got.Source, got.Reason, Inline)
	}
	if string(got.Bundle) != string(inline) {
		t.Errorf("the bundle: got %q, want %q", got.Bundle, inline)
	}
}

// A firing the master kept without its bundle is still a firing. A write that was
// tried and failed is reported apart from a master that captured nothing, because
// the first says the bundle was lost and the second says none ever existed.
func TestAFailedWriteIsReportedApartFromAMasterThatCapturedNothing(t *testing.T) {
	cases := map[string]struct{ source, reason string }{
		"encode":  {Unretained, "could not encode"},
		"write":   {Unretained, "could not write"},
		"unknown": {Unretained, `reported "unknown"`},
		"":        {Unrecorded, "captured no evidence"},
	}
	for errValue, want := range cases {
		row := Row{FiringID: "ee", EvidenceError: errValue}
		got := row.Resolve(t.TempDir())
		if got.Source != want.source {
			t.Errorf("evidence_error %q: got %q, want %q", errValue, got.Source, want.source)
		}
		if !strings.Contains(got.Reason, want.reason) {
			t.Errorf("evidence_error %q: reason %q must contain %q",
				errValue, got.Reason, want.reason)
		}
	}
}

// The counting reader keeps a row's stamps; this one also keeps what a judgment
// reads: the locators, the bundle and the blocked input.
func TestLoadKeepsTheFieldsACountingReaderDrops(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, AuditLogName), `{"firing_id":"f1","hook_name":"g","harness":"claude-code","tool_name":"Bash","snippet":"cd x && cat y","repository":"/repo","rule":"r","hook_digest":"hd","rule_hash":"rh","master_build_digest":"mb","locators":{"tool_use_id":"toolu_1","transcript_path":"/t.jsonl"},"evidence":{"payload":{"tool_name":"Bash"}},"evidence_complete":true}
not json
{"firing_id":"f1","hook_name":"g","snippet":"a duplicate of the same firing"}
{"hook_name":"g","harness":"codex","session_id":"s","timestamp":"t1","snippet":"an older row"}
`)

	rows, present := Load(dir)
	if !present {
		t.Fatal("a readable stream must report present")
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: a damaged line is skipped and a firing id dedupes", len(rows))
	}

	r := rows[0]
	if r.Locators["tool_use_id"] != "toolu_1" {
		t.Errorf("the call identifier: got %q, want toolu_1", r.Locators["tool_use_id"])
	}
	if r.Locators["transcript_path"] != "/t.jsonl" {
		t.Errorf("the transcript locator: got %q", r.Locators["transcript_path"])
	}
	if _, supplied := r.Locators["call_id"]; supplied {
		t.Error("a locator the harness never supplied must be absent, not empty")
	}
	if r.Snippet != "cd x && cat y" {
		t.Errorf("the blocked input: got %q", r.Snippet)
	}
	if r.ToolName != "Bash" || r.Repository != "/repo" || r.HookDigest != "hd" {
		t.Errorf("a row must keep its tool, repository and stamps: %+v", r)
	}
	if got := r.Resolve(dir); got.Source != Inline {
		t.Errorf("the inline bundle survived the read: got %q", got.Source)
	}
	if rows[1].Snippet != "an older row" {
		t.Errorf("a row with no identifier is still read: got %q", rows[1].Snippet)
	}
}

// An absent store is not an empty one: a reader must tell them apart before saying
// a guard never fired.
func TestLoadSeparatesAnAbsentStoreFromAnEmptyOne(t *testing.T) {
	dir := t.TempDir()
	if _, present := Load(filepath.Join(dir, "nothing-here")); present {
		t.Error("an absent stream must not report present")
	}
	write(t, filepath.Join(dir, AuditLogName), "")
	rows, present := Load(dir)
	if !present {
		t.Error("an empty stream must report present")
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows from an empty stream", len(rows))
	}
}

// Only an identifier names one row, so Find refuses the empty one rather than
// returning whichever row happens to carry no identifier.
func TestFindRefusesTheEmptyIdentifier(t *testing.T) {
	rows := []Row{{Snippet: "a legacy row"}, {FiringID: "x", Snippet: "a stamped row"}}

	if _, ok := Find(rows, ""); ok {
		t.Error("the empty identifier must match no row")
	}
	got, ok := Find(rows, "x")
	if !ok || got.Snippet != "a stamped row" {
		t.Errorf("an identifier must find its row: got %+v, ok=%t", got, ok)
	}
	if _, ok := Find(rows, "absent"); ok {
		t.Error("an identifier no row carries must miss")
	}
}

// A link below the store reads as damage and reports nothing about its target,
// whether or not the target matches the row's stamps.
func TestALinkedSidecarIsDamagedAndReportsNothingOfItsTarget(t *testing.T) {
	const secret = `{"secret":"outside the store"}`
	sum := sha256.Sum256([]byte(secret))
	digest := hex.EncodeToString(sum[:])
	ref := PayloadsDir + "/x.json"

	cases := []struct {
		name  string
		link  func(t *testing.T, store, outside string)
		bytes int
		sha   string
		named string
	}{
		{
			name: "a link at the sidecar whose target matches the row",
			link: func(t *testing.T, store, outside string) {
				symlink(t, filepath.Join(outside, "secret.json"), filepath.Join(store, PayloadsDir, "x.json"))
			},
			bytes: len(secret), sha: digest, named: ref,
		},
		{
			name: "a link at the sidecar whose target differs from the row",
			link: func(t *testing.T, store, outside string) {
				symlink(t, filepath.Join(outside, "secret.json"), filepath.Join(store, PayloadsDir, "x.json"))
			},
			bytes: 1, sha: "feed", named: ref,
		},
		{
			name: "a link at the payload directory",
			link: func(t *testing.T, store, outside string) {
				write(t, filepath.Join(outside, "x.json"), secret)
				if err := os.Remove(filepath.Join(store, PayloadsDir)); err != nil {
					t.Fatal(err)
				}
				symlink(t, outside, filepath.Join(store, PayloadsDir))
			},
			bytes: len(secret), sha: digest, named: PayloadsDir,
		},
		{
			name: "a link that stays inside the store",
			link: func(t *testing.T, store, _ string) {
				write(t, filepath.Join(store, "other.json"), secret)
				symlink(t, filepath.Join("..", "other.json"), filepath.Join(store, PayloadsDir, "x.json"))
			},
			bytes: len(secret), sha: digest, named: ref,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, outside := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(store, PayloadsDir), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(outside, "secret.json"), secret)
			c.link(t, store, outside)

			row := Row{FiringID: "aa", EvidenceRef: ref, EvidenceBytes: c.bytes, EvidenceSHA256: c.sha}
			got := row.Resolve(store)
			if got.Source != Damaged {
				t.Fatalf("got %q (%s), want %q", got.Source, got.Reason, Damaged)
			}
			if len(got.Bundle) != 0 {
				t.Errorf("a linked sidecar handed back %q", got.Bundle)
			}
			if !strings.Contains(got.Reason, c.named+" is a symlink") {
				t.Errorf("the reason does not name the link %s: %s", c.named, got.Reason)
			}
			for _, leak := range []string{digest, outside, strconv.Itoa(len(secret))} {
				if strings.Contains(got.Reason, leak) {
					t.Errorf("the reason reports %q about the target: %s", leak, got.Reason)
				}
			}
		})
	}
}

// The store directory itself may be the operator's link; only what lies below it
// must be the store's own.
//
// pins existing behaviour
func TestALinkedStoreDirectoryStillResolvesItsSidecar(t *testing.T) {
	real := t.TempDir()
	row := sidecarRow(t, real, "aa", `{"payload":{}}`)
	link := filepath.Join(t.TempDir(), "hooks")
	symlink(t, real, link)

	if got := row.Resolve(link); got.Source != Sidecar {
		t.Errorf("got %q (%s), want %q", got.Source, got.Reason, Sidecar)
	}
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}
