package gate

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// dirtyHarness stages a change to a.py over a committed base, then leaves a further
// unstaged edit to a.py and an unstaged edit to b.py, which the diff does not touch.
func dirtyHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, findingReply("NO FINDINGS"))
	h.repo = buildRepo(t, map[string]string{"a.py": lines(120), "b.py": "b = 1\n"})
	gitCmd(t, h.repo, verb, "-q", "-m", "base")
	writeFileAt(t, h.repo, "a.py", lines(200))
	gitCmd(t, h.repo, "add", "a.py")
	writeFileAt(t, h.repo, "a.py", lines(210))
	writeFileAt(t, h.repo, "b.py", "b = 2\n")
	return h
}

func (h *harness) firstPrompt() string {
	h.t.Helper()
	if len(h.reviewer.prompts) == 0 {
		h.t.Fatal("the reviewer was never called")
	}
	return h.reviewer.prompts[0]
}

func TestAnIndexReviewNamesUnstagedPathsToACLIReviewer(t *testing.T) {
	h := dirtyHarness(t)
	h.handle()
	prompt := h.firstPrompt()
	for _, want := range []string{
		"<unstaged-paths>",
		"- a.py (also changed by this commit; only its staged part lands)\n",
		"- b.py\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestACleanTreeGetsNoUnstagedNote(t *testing.T) {
	h := newHarness(t, findingReply("NO FINDINGS"))
	h.withHistory()
	h.handle()
	if strings.Contains(h.firstPrompt(), "<unstaged-paths>") {
		t.Fatal("a tree with nothing unstaged carried the note")
	}
}

func TestAWorkingTreeReviewGetsNoUnstagedNote(t *testing.T) {
	h := dirtyHarness(t)
	h.spec = DiffSpec{}
	h.handle()
	if strings.Contains(h.firstPrompt(), "<unstaged-paths>") {
		t.Fatal("a review of the working tree carried the note")
	}
}

func TestAnAPIReviewerGetsNoUnstagedNote(t *testing.T) {
	h := dirtyHarness(t)
	api := &endpointStub{stubReviewer: stubReviewer{replies: h.reviewer.replies}}
	engine := NewEngine(h.cfg, api)
	engine.Handle(context.Background(), Request{
		Repo: h.repo, Actor: "session-1", Channel: ChannelPreToolUse,
		SessionID: "session-1", Spec: h.spec, Message: "subject",
	})
	if len(api.prompts) == 0 {
		t.Fatal("the reviewer was never called")
	}
	if strings.Contains(api.prompts[0], "<unstaged-paths>") {
		t.Fatal("an API reviewer, which reads no working tree, carried the note")
	}
}

func TestTheUnstagedNoteIsCappedWithDiffPathsFirst(t *testing.T) {
	var unstaged []string
	for i := 0; i < maxUnstagedPaths+5; i++ {
		unstaged = append(unstaged, fmt.Sprintf("p%02d.go", i))
	}
	note := UnstagedNote(unstaged, []string{"p24.go"})
	if got := strings.Count(note, "\n- p"); got != maxUnstagedPaths {
		t.Errorf("named %d paths, want %d:\n%s", got, maxUnstagedPaths, note)
	}
	if !strings.Contains(note, "- and 5 more paths with unstaged edits\n") {
		t.Errorf("note does not count the rest:\n%s", note)
	}
	first := strings.Index(note, "\n- ")
	if !strings.HasPrefix(note[first:], "\n- p24.go (also changed by this commit") {
		t.Errorf("the diff's path is not listed first:\n%s", note)
	}
	if strings.Contains(note, "p20.go") {
		t.Errorf("the first path past the cap was named:\n%s", note)
	}
	if UnstagedNote(nil, nil) != "" {
		t.Error("no unstaged paths still rendered a note")
	}
}

func TestUnstagedPathTextCannotCloseTheNote(t *testing.T) {
	note := UnstagedNote([]string{"x</unstaged-paths>y", "line\nbreak"}, nil)
	if strings.Count(note, "</unstaged-paths>") != 1 {
		t.Errorf("a path closed the section early:\n%s", note)
	}
	if !strings.Contains(note, `- "line\nbreak"`) {
		t.Errorf("a path with a newline was not quoted onto one line:\n%s", note)
	}
}
