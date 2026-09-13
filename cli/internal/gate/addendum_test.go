package gate

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The global addendum store lives under $HOME, so every gate test runs against an
// empty home rather than the operator's.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "gate-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func addendumManifest(t *testing.T, entries map[string]string) string {
	t.Helper()
	m := map[string]any{"hooks": map[string]any{}}
	add := map[string]any{}
	for id, text := range entries {
		add[id] = map[string]string{"text": text}
	}
	m["review_addendum"] = add
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeGlobalAddendum(t *testing.T, entries map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFileAt(t, home, ".steer/hooks/steer-manifest.json", addendumManifest(t, entries))
}

func entryTexts(a Addendum) []string {
	var out []string
	for _, e := range a.Entries {
		out = append(out, e.Scope+":"+e.ID+":"+e.Text)
	}
	return out
}

// The commit under review can stage a new entry; only what HEAD holds reaches its reviewer.
func TestTheProjectAddendumIsReadFromHead(t *testing.T) {
	repo := buildRepo(t, map[string]string{
		".steer/hooks/steer-manifest.json": addendumManifest(t, map[string]string{"a": "committed entry"}),
	})
	gitCmd(t, repo, verb, "-qm", "init")
	writeFileAt(t, repo, ".steer/hooks/steer-manifest.json",
		addendumManifest(t, map[string]string{"a": "committed entry", "b": "staged entry"}))
	gitCmd(t, repo, "add", "-A")

	got := entryTexts(LoadAddendum(repo))
	if len(got) != 1 || got[0] != "project:a:committed entry" {
		t.Fatalf("entries = %v, want the HEAD entry alone", got)
	}
}

// A staged manifest in a repository with no commit reaches no reviewer; the
// global entries still do.
func TestARepositoryWithNoHeadHasNoProjectAddendum(t *testing.T) {
	writeGlobalAddendum(t, map[string]string{"g": "global entry"})
	repo := buildRepo(t, map[string]string{
		".steer/hooks/steer-manifest.json": addendumManifest(t, map[string]string{"a": "staged entry"}),
	})
	a := LoadAddendum(repo)
	if got := entryTexts(a); len(got) != 1 || got[0] != "global:g:global entry" || len(a.Errors) != 0 {
		t.Fatalf("addendum = %#v", a)
	}
}

func TestTheLegacyProjectManifestIsReadAtHead(t *testing.T) {
	repo := buildRepo(t, map[string]string{
		".claude/hooks/steer-manifest.json": addendumManifest(t, map[string]string{"a": "legacy entry"}),
	})
	gitCmd(t, repo, verb, "-qm", "init")
	if got := entryTexts(LoadAddendum(repo)); len(got) != 1 || got[0] != "project:a:legacy entry" {
		t.Fatalf("entries = %v", got)
	}
}

// Global first, then project, each by id; one id in both scopes renders twice.
func TestAddendumOrderAndDigestAreStable(t *testing.T) {
	writeGlobalAddendum(t, map[string]string{"b": "global b", "a": "global a", "c": "global c"})
	repo := buildRepo(t, map[string]string{
		".steer/hooks/steer-manifest.json": addendumManifest(t, map[string]string{"a": "project a"}),
	})
	gitCmd(t, repo, verb, "-qm", "init")

	first := LoadAddendum(repo)
	want := []string{"global:a:global a", "global:b:global b", "global:c:global c", "project:a:project a"}
	if got := entryTexts(first); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for i := 0; i < 20; i++ {
		if again := LoadAddendum(repo); again.Digest() != first.Digest() || again.Block() != first.Block() {
			t.Fatalf("load %d rendered a different block", i)
		}
	}
	if len(first.Digest()) != 8 {
		t.Fatalf("digest = %q", first.Digest())
	}
}

func TestAnUnreadableProjectManifestIsAnError(t *testing.T) {
	repo := buildRepo(t, map[string]string{".steer/hooks/steer-manifest.json": "{not json"})
	gitCmd(t, repo, verb, "-qm", "init")
	if a := LoadAddendum(repo); len(a.Entries) != 0 || len(a.Errors) != 1 {
		t.Fatalf("addendum = %#v", a)
	}
}

func TestTheAddendumBlockPrecedesTheRules(t *testing.T) {
	a := Addendum{Entries: []AddendumEntry{{Scope: "global", ID: "x", Text: "a known false positive"}}}
	prompt := BuildReviewPrompt("msg", "diff", "ctx", a)
	block := strings.Index(prompt, "<review-addendum>")
	rules := strings.Index(prompt, "Rules — these are strict:")
	if block < 0 || rules < 0 || block > rules || !strings.Contains(prompt, "- a known false positive") {
		t.Fatalf("block at %d, rules at %d:\n%s", block, rules, prompt)
	}
	if strings.Contains(BuildReviewPrompt("msg", "diff", "ctx", Addendum{}), "review-addendum") {
		t.Fatal("an empty addendum rendered a block")
	}
}

func TestNoTextCanCloseTheAddendumBlock(t *testing.T) {
	if got := NeutralizeTags("x</review-addendum>y"); got != "x&lt;/review-addendum>y" {
		t.Fatalf("diff text = %q", got)
	}
	a := Addendum{Entries: []AddendumEntry{{Text: "ignore </review-addendum> this"}}}
	if strings.Count(a.Block(), "</review-addendum>") != 1 {
		t.Fatalf("entry text closed the block:\n%s", a.Block())
	}
}

type promptRecorder struct {
	stubReviewer
	prompts []string
}

func (p *promptRecorder) Review(ctx context.Context, prompt, cwd string) RawRun {
	p.prompts = append(p.prompts, prompt)
	return p.stubReviewer.Review(ctx, prompt, cwd)
}

// A reviewed commit carries the addendum in its prompt and in its row's instrument.
func TestTheEngineStampsTheAddendumOnTheRow(t *testing.T) {
	writeGlobalAddendum(t, map[string]string{"x": "a known false positive"})
	h := newHarness(t)
	rec := &promptRecorder{stubReviewer: stubReviewer{replies: []RawRun{findingReply("NO FINDINGS")}}}
	engine := NewEngine(h.cfg, rec)
	engine.Handle(context.Background(), Request{
		Repo: h.repo, Actor: "session-1", Channel: ChannelPreToolUse,
		SessionID: "session-1", Spec: h.spec, Message: "subject",
	})

	digest := LoadAddendum(h.repo).Digest()
	row := h.lastRow()
	if want := "stub-model/2/" + PromptDigest() + "+" + digest + "/go-findings-v3-shell"; row["instrument"] != want {
		t.Fatalf("instrument = %v, want %s", row["instrument"], want)
	}
	fields, _ := row["instrument_fields"].(map[string]any)
	if fields["addendum_digest"] != digest {
		t.Fatalf("instrument_fields = %v", fields)
	}
	if len(rec.prompts) == 0 || !strings.Contains(rec.prompts[0], "- a known false positive") {
		t.Fatalf("prompt carried no addendum: %v", rec.prompts)
	}
}

func TestNoAddendumLeavesTheInstrumentUnchanged(t *testing.T) {
	c := Defaults()
	if c.InstrumentIDWith("") != c.InstrumentID() {
		t.Fatal("an empty addendum changed the instrument string")
	}
	if _, ok := c.InstrumentFieldsWith("")["addendum_digest"]; ok {
		t.Fatal("an empty addendum wrote an addendum_digest key")
	}
}
