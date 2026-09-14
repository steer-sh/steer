package cadence

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/archive"
	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
)

// writeManifest places a steer manifest in a store directory.
func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steer-manifest.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func identity(t *testing.T, root string) string {
	t.Helper()
	id, err := checks.RepoIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ruleRecord(scope string) mine.Record {
	return mine.Record{"tier": "instruction", "status": "active", "rule_id": "config-path",
		"rule_scope": scope, "rule_hash": digest("resolve config from the repo root")}
}

func wantState(t *testing.T, got *Artifact, state string) {
	t.Helper()
	if got == nil || got.State != state {
		t.Fatalf("artifact = %+v; want state %q", got, state)
	}
}

// A rule is read against the text its record froze: the same text is present,
// other text under the same id is changed, a store that dropped it is absent even
// when the section an agent reads was synced to match, and a corrupt store says so.
func TestArtifactReadsARuleAgainstTheTextTheRecordFroze(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".steer", "hooks")
	rec := ruleRecord("global")

	writeManifest(t, store, `{"rules":{"config-path":{"text":"resolve  config from the repo root"}}}`)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactPresent)

	writeManifest(t, store, `{"rules":{"config-path":{"text":"resolve config from the cwd"}}}`)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactChanged)

	writeManifest(t, store, `{"rules":{}}`)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactAbsent)

	writeManifest(t, store, `{"rules":`)
	got := ArtifactOf(rec, ArtifactSources{})
	wantState(t, got, ArtifactUnreadable)
	if got.Detail == "" {
		t.Error("an unreadable store names why")
	}
}

// A project entry is read only in the repository its record names. Another checkout
// holding the same id is not that entry, and a record that names no repository is
// never matched against whichever checkout the reader stands in.
func TestArtifactPlacesAProjectEntryByItsRepository(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b} {
		writeManifest(t, filepath.Join(root, ".steer", "hooks"),
			`{"rules":{"config-path":{"text":"resolve config from the repo root"}}}`)
	}
	rec := ruleRecord("project")
	rec["rule_repo"] = identity(t, a)

	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: a}), ArtifactPresent)
	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: b}), ArtifactElsewhere)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactElsewhere)

	delete(rec, "rule_repo")
	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: a}), ArtifactUnplaced)
}

// A review entry is read from what the commit gate reads, so a project entry
// removed from HEAD is absent whatever the working tree holds.
func TestArtifactReadsAnAddendumEntryAsTheCommitGateReadsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	rec := mine.Record{"tier": "review", "status": "active", "addendum_id": "fp-sort",
		"addendum_scope": "project", "addendum_repo": identity(t, root),
		"addendum_hash": digest("sorted output is not a finding")}
	at := func(a *gate.Addendum) *Artifact {
		return ArtifactOf(rec, ArtifactSources{RepoRoot: root, Addendum: a})
	}

	wantState(t, at(&gate.Addendum{Entries: []gate.AddendumEntry{
		{Scope: "project", ID: "fp-sort", Text: "sorted output is not a finding"}}}), ArtifactPresent)
	wantState(t, at(&gate.Addendum{Entries: []gate.AddendumEntry{
		{Scope: "project", ID: "fp-sort", Text: "sorted output is fine"}}}), ArtifactChanged)
	wantState(t, at(&gate.Addendum{Entries: []gate.AddendumEntry{
		{Scope: "global", ID: "fp-sort", Text: "sorted output is not a finding"}}}), ArtifactAbsent)
	wantState(t, at(&gate.Addendum{Errors: []string{"review addendum: project store: bad json"}}), ArtifactUnreadable)
	wantState(t, at(nil), ArtifactUnreadable)
}

// A guard is read from its store's hooks section by the rule digest it froze.
func TestArtifactReadsAGuardEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".steer", "hooks")
	rec := mine.Record{"tier": "guard", "status": "active", "hook_id": "no-bare-python",
		"hook_scope": "global", "rule_hash": "h1"}

	writeManifest(t, store, `{"hooks":{"no-bare-python":{"rule":"use python3","rule_hash":"h1"}}}`)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactPresent)

	writeManifest(t, store, `{"hooks":{}}`)
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactAbsent)

	delete(rec, "hook_id")
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactUnbound)
}

// A check is read from the repository its record binds, against the detector the
// record registered.
func TestArtifactReadsACheckDeclaration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, other := t.TempDir(), t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, ".steer", "checks", "no-count.sh")
	if err := os.WriteFile(script, []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	decl := checks.Declaration{ID: "no-count", Kind: checks.KindScript, Script: ".steer/checks/no-count.sh"}
	m, err := checks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Checks = append(m.Checks, decl)
	if err := checks.Save(root, m); err != nil {
		t.Fatal(err)
	}
	det, err := checks.DetectorOf(root, decl)
	if err != nil {
		t.Fatal(err)
	}
	rec := mine.Record{"tier": "check", "status": "active", mine.CheckIDKey: "no-count",
		mine.CheckRepoKey: identity(t, root), mine.CheckDigestKey: det.Digest}

	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: root}), ArtifactPresent)
	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: other}), ArtifactElsewhere)

	if err := os.WriteFile(script, []byte("exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: root}), ArtifactChanged)

	m.Checks = nil
	if err := checks.Save(root, m); err != nil {
		t.Fatal(err)
	}
	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: root}), ArtifactAbsent)

	delete(rec, mine.CheckIDKey)
	wantState(t, ArtifactOf(rec, ArtifactSources{RepoRoot: root}), ArtifactUnbound)
}

// A declined record installed nothing, so it has no artifact to read, while the same
// record still active does.
func TestArtifactIsNilForADeclinedRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rec := ruleRecord("global")
	wantState(t, ArtifactOf(rec, ArtifactSources{}), ArtifactAbsent)
	rec["status"] = "rejected"
	if got := ArtifactOf(rec, ArtifactSources{}); got != nil {
		t.Fatalf("artifact = %+v; want nil", got)
	}
}

// A refresh queues a record whose fix is gone, and carries the reading with it.
func TestRefreshQueuesARecordWhoseFixIsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeManifest(t, filepath.Join(home, ".steer", "hooks"), `{"rules":{}}`)
	state := t.TempDir()
	rec := ruleRecord("global")
	for k, v := range map[string]any{"id": "config-path-loss", "class": "config path loss",
		"match": "config path", "kind": "defect", "target": "CLAUDE.md",
		"unit": mine.UnitChain, "corpus": mine.CorpusReviewFindings, "installed_ts": 1000,
		"scope": map[string]any{"version": 1, "all": true}} {
		rec[k] = v
	}
	if err := mine.Save(state, []mine.Record{rec}); err != nil {
		t.Fatal(err)
	}
	q, err := Refresh(Options{GateStateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Interventions) != 1 {
		t.Fatalf("queue interventions = %+v; want the record whose rule is gone", q.Interventions)
	}
	wantState(t, q.Interventions[0].Artifact, ArtifactAbsent)

	writeManifest(t, filepath.Join(home, ".steer", "hooks"),
		`{"rules":{"config-path":{"text":"resolve config from the repo root"}}}`)
	if q, err = Refresh(Options{GateStateDir: state}); err != nil || len(q.Interventions) != 0 {
		t.Fatalf("with the rule in place the queue holds %+v (err %v); want none", q.Interventions, err)
	}
}

// archiveAt writes one archive entry by hand, so a test sets its removal time.
func archiveAt(t *testing.T, store, kind, id, removed string) {
	t.Helper()
	dir := filepath.Join(store, archive.DirName, id, removed)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"tier":"` + kind + `","id":"` + id + `","scope":"global","removed":"` + removed + `"}`
	if err := os.WriteFile(filepath.Join(dir, archive.EntryName), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// split2026 is 2026-01-01T00:00:00Z, the install time the removal tests freeze.
const split2026 = 1767225600

// A fix in place that its store archived after the record's split carries the
// removal beside its state, whether its text is the frozen one or not. An entry
// of another tier under the same id is not its removal.
func TestArtifactCarriesARemovalAfterTheSplit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".steer", "hooks")
	rec := ruleRecord("global")
	rec["installed_ts"] = split2026

	writeManifest(t, store, `{"rules":{"config-path":{"text":"resolve config from the repo root"}}}`)
	archiveAt(t, store, "guard", "config-path", "2026-02-01T00:00:00.000000000Z")
	if got := ArtifactOf(rec, ArtifactSources{}); got.RemovalRecorded() {
		t.Fatalf("a guard's archive entry read as the rule's removal: %+v", got)
	}

	archiveAt(t, store, "instruction", "config-path", "2026-03-01T00:00:00.000000000Z")
	got := ArtifactOf(rec, ArtifactSources{})
	wantState(t, got, ArtifactPresent)
	if got.Removed != "2026-03-01T00:00:00.000000000Z" {
		t.Fatalf("removed = %q; want the rule's archived removal", got.Removed)
	}

	writeManifest(t, store, `{"rules":{"config-path":{"text":"resolve config from the cwd"}}}`)
	got = ArtifactOf(rec, ArtifactSources{})
	wantState(t, got, ArtifactChanged)
	if !got.RemovalRecorded() {
		t.Fatal("a changed rule lost its recorded removal")
	}
}

// A removal counts only after the split, compared in whole seconds: one in the
// split's own second predates a record registered after a restore.
func TestARemovalInTheSplitSecondIsNotRecorded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".steer", "hooks")
	rec := ruleRecord("global")
	rec["installed_ts"] = split2026
	writeManifest(t, store, `{"rules":{"config-path":{"text":"resolve config from the repo root"}}}`)

	archiveAt(t, store, "instruction", "config-path", "2025-12-31T23:00:00.000000000Z")
	archiveAt(t, store, "instruction", "config-path", "2026-01-01T00:00:00.500000000Z")
	if got := ArtifactOf(rec, ArtifactSources{}); got.RemovalRecorded() {
		t.Fatalf("a removal before or in the split second was recorded: %+v", got)
	}
	archiveAt(t, store, "instruction", "config-path", "2026-01-01T00:00:01.000000000Z")
	if got := ArtifactOf(rec, ArtifactSources{}); !got.RemovalRecorded() {
		t.Fatalf("a removal a second after the split was missed: %+v", got)
	}
}

// A review entry and a check read their own store's archive: the global hook
// store for a global entry, the repository's check store for a check.
func TestArtifactReadsTheArchiveOfAnAddendumAndACheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archiveAt(t, filepath.Join(home, ".steer", "hooks"), "review", "fp-sort", "2026-02-01T00:00:00.000000000Z")
	rev := mine.Record{"tier": "review", "status": "active", "addendum_id": "fp-sort",
		"addendum_scope": "global", "installed_ts": split2026}
	got := ArtifactOf(rev, ArtifactSources{Addendum: &gate.Addendum{Entries: []gate.AddendumEntry{
		{Scope: "global", ID: "fp-sort", Text: "sorted output is not a finding"}}}})
	wantState(t, got, ArtifactPresent)
	if !got.RemovalRecorded() {
		t.Fatalf("the addendum entry's removal was missed: %+v", got)
	}

	root := t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".steer", "checks", "no-count.sh"), []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	decl := checks.Declaration{ID: "no-count", Kind: checks.KindScript, Script: ".steer/checks/no-count.sh"}
	m, err := checks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Checks = append(m.Checks, decl)
	if err := checks.Save(root, m); err != nil {
		t.Fatal(err)
	}
	det, err := checks.DetectorOf(root, decl)
	if err != nil {
		t.Fatal(err)
	}
	archiveAt(t, checks.Dir(root), "check", "no-count", "2026-02-01T00:00:00.000000000Z")
	chk := mine.Record{"tier": "check", "status": "active", mine.CheckIDKey: "no-count",
		mine.CheckRepoKey: identity(t, root), mine.CheckDigestKey: det.Digest, "installed_ts": split2026}
	got = ArtifactOf(chk, ArtifactSources{RepoRoot: root})
	wantState(t, got, ArtifactPresent)
	if !got.RemovalRecorded() {
		t.Fatalf("the check's removal was missed: %+v", got)
	}
}

// A refresh queues a record whose fix is in place but was archived after the split.
func TestRefreshQueuesARecordWhoseFixWasRemovedSinceItsSplit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".steer", "hooks")
	writeManifest(t, store, `{"rules":{"config-path":{"text":"resolve config from the repo root"}}}`)
	state := t.TempDir()
	rec := ruleRecord("global")
	for k, v := range map[string]any{"id": "config-path-loss", "class": "config path loss",
		"match": "config path", "kind": "defect", "target": "CLAUDE.md",
		"unit": mine.UnitChain, "corpus": mine.CorpusReviewFindings, "installed_ts": split2026,
		"scope": map[string]any{"version": 1, "all": true}} {
		rec[k] = v
	}
	if err := mine.Save(state, []mine.Record{rec}); err != nil {
		t.Fatal(err)
	}

	archiveAt(t, store, "instruction", "config-path", "2026-02-01T00:00:00.000000000Z")
	q, err := Refresh(Options{GateStateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Interventions) != 1 || !q.Interventions[0].Artifact.RemovalRecorded() {
		t.Fatalf("queue interventions = %+v; want the record whose rule was removed and is back", q.Interventions)
	}
}
