package gate

import (
	"context"
	"sync"
	"testing"
)

// Outcome classification. A denied tool call does not stop a run: the model reads the
// refusal, answers in full, and the provider stamps status ERROR on the whole
// invocation. So the status alone cannot say whether a review happened.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		raw  RawRun
		want RunOutcome
		text bool
	}{
		{"success", RawRun{Response: oneFinding, Status: "SUCCESS"}, OutcomeOK, true},
		{"unknown severity", RawRun{Response: "[1] SEVERITY: NONE\n    FILE: a.go\n" +
			"    CLAIM: x.\n    FAILS WHEN: y.", Status: "SUCCESS"}, OutcomeLost, false},
		{"missing required field", RawRun{Response: "[1] SEVERITY: BLOCKING\n" +
			"    FILE: a.go\n    CLAIM: x.", Status: "SUCCESS"}, OutcomeLost, false},
		{"empty", RawRun{Status: "ERROR"}, OutcomeEmpty, false},
		{"findings under error", RawRun{Response: oneFinding, Status: "ERROR"}, OutcomeSalvaged, true},
		{"clean reply under error", RawRun{Response: "NO FINDINGS", Status: "ERROR",
			Error: "permission check failed"}, OutcomeSalvagedClean, true},
		{"prose under error", RawRun{Response: "I could not run ls.", Status: "ERROR"}, OutcomeLost, false},
		{"clean words opening a failure report", RawRun{
			Response: "NO FINDINGS could be determined — the tool call was denied.",
			Status:   "ERROR"}, OutcomeLost, false},
		{"decorated clean reply", RawRun{Response: "**NO FINDINGS.**", Status: "ERROR"},
			OutcomeSalvagedClean, true},
		{"terminal outcome wins", RawRun{Response: oneFinding, Outcome: OutcomeTimeout}, OutcomeTimeout, false},
		// A refusal is the shape a run takes when its tool calls were denied and it
		// reported that instead of reviewing. Under SUCCESS it used to count as a
		// reviewer who found nothing, which clears the commit on nobody's evidence.
		{"refusal under success", RawRun{
			Response: "I attempted to run `git status`, but execution was blocked by a deny rule.",
			Status:   "SUCCESS"}, OutcomeLost, false},
		{"clean reply under success", RawRun{Response: "NO FINDINGS", Status: "SUCCESS"}, OutcomeOK, true},
		{"decorated clean reply under success", RawRun{Response: "**NO FINDINGS.**",
			Status: "SUCCESS"}, OutcomeOK, true},
		// 9eb015c's undercount hit clean commits only, so the loose reader stays: a
		// clean reply keeps counting when the reviewer adds a closing sentence.
		{"clean reply with a closing remark under success", RawRun{
			Response: "NO FINDINGS. The diff is well covered.", Status: "SUCCESS"}, OutcomeOK, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := Classify(c.raw)
			if run.Outcome != c.want {
				t.Errorf("Outcome = %q, want %q", run.Outcome, c.want)
			}
			if run.Outcome == OutcomeLost && run.Detail == "" {
				t.Errorf("Detail is empty; a lost run must say what it dropped")
			}
			if (run.Text != "") != c.text {
				t.Errorf("Text = %q, want text: %v", run.Text, c.text)
			}
		})
	}
}

// The denial must stay in the ledger. Collapsing a salvaged clean run into ok would
// erase the only record that the provider errored.
func TestSalvagedCleanKeepsItsDetail(t *testing.T) {
	run := Classify(RawRun{Response: "NO FINDINGS", Status: "ERROR", Error: "denied"})
	if run.Detail != "denied" {
		t.Fatalf("Detail = %q, want denied", run.Detail)
	}
	row := ReviewerReport(Defaults(), []ReviewRun{run})
	errors, ok := row["reviewer_errors"].([]string)
	if !ok || len(errors) != 1 {
		t.Fatalf("reviewer_errors = %v, want one entry", row["reviewer_errors"])
	}
}

// A clean run answers. It carries no finding, so it must not reach the composed review.
func TestPanelCountsCleanSalvagedRunAsAnswered(t *testing.T) {
	cfg := Defaults()
	cfg.ReviewerRuns = 1
	panel := NewPanel(cfg, &stubReviewer{replies: []RawRun{
		{Response: "NO FINDINGS", Status: "ERROR", Error: "denied"}}})
	result := panel.Run(context.Background(), "prompt", t.TempDir(), DiffSpec{})
	if result.Answered != 1 {
		t.Errorf("Answered = %d, want 1", result.Answered)
	}
	if len(result.Reviews) != 0 {
		t.Errorf("Reviews = %v, want none", result.Reviews)
	}
}

// A reply may open with the clean words and still carry a finding below them. The
// finding decides, so the opener can never bury it.
func TestPanelKeepsFindingsUnderACleanOpener(t *testing.T) {
	cfg := Defaults()
	cfg.ReviewerRuns = 1
	body := "NO FINDINGS in a.py, but in b.py:\n\n" + oneFinding
	panel := NewPanel(cfg, &stubReviewer{replies: []RawRun{findingReply(body)}})
	result := panel.Run(context.Background(), "prompt", t.TempDir(), DiffSpec{})
	if len(result.Reviews) != 1 {
		t.Fatalf("Reviews = %v, want the finding kept", result.Reviews)
	}
}

// A decorated clean reply must be clean to BOTH readers. It was clean to Classify and
// a finding to the panel filter, which denied a commit no reviewer objected to.
func TestPanelDropsDecoratedCleanReply(t *testing.T) {
	cfg := Defaults()
	cfg.ReviewerRuns = 1
	for _, body := range []string{"**NO FINDINGS**", "`NO FINDINGS`", "**NO FINDINGS.**", "NO FINDINGS\n"} {
		panel := NewPanel(cfg, &stubReviewer{replies: []RawRun{findingReply(body)}})
		result := panel.Run(context.Background(), "prompt", t.TempDir(), DiffSpec{})
		if len(result.Reviews) != 0 {
			t.Errorf("body %q: Reviews = %v, want none", body, result.Reviews)
		}
		if result.Answered != 1 {
			t.Errorf("body %q: Answered = %d, want 1", body, result.Answered)
		}
	}
}

// An endpoint can answer a configured model name with another model, so the ledger
// row counts the models that answered beside the configured one.
func TestReviewerReportCountsTheModelsThatAnswered(t *testing.T) {
	runs := []ReviewRun{
		Classify(RawRun{Response: "NO FINDINGS", Status: "SUCCESS", ResponseModel: "m-1"}),
		Classify(RawRun{Outcome: OutcomeTruncated, Detail: "cut", ResponseModel: "m-1"}),
		Classify(RawRun{Outcome: OutcomeNoKey, Detail: "no key"}),
	}
	row := ReviewerReport(Defaults(), runs)
	models, _ := row["reviewer_response_models"].(map[string]int)
	if models["m-1"] != 2 || len(models) != 1 {
		t.Fatalf("reviewer_response_models = %v, want m-1 twice", row["reviewer_response_models"])
	}
	if _, ok := ReviewerReport(Defaults(), runs[2:])["reviewer_response_models"]; ok {
		t.Error("a panel with no reported model wrote the field")
	}
}

// The endpoint joins the instrument only for an API reviewer, so a CLI reviewer's
// rows keep the fields they always had.
func TestInstrumentFieldsNameTheEndpointOnlyWhenSet(t *testing.T) {
	cfg := Defaults()
	if _, ok := cfg.InstrumentFields()["endpoint"]; ok {
		t.Error("a CLI reviewer's instrument names an endpoint")
	}
	cfg.ReviewerEndpoint = "openrouter.ai"
	if got := cfg.InstrumentFields()["endpoint"]; got != "openrouter.ai" {
		t.Errorf("endpoint = %v", got)
	}
}

// viewStub records the view a panel hands it.
type viewStub struct {
	wants bool
	mu    sync.Mutex
	views []*CommitView
	plain int
}

func (v *viewStub) Name() string       { return "view-stub" }
func (v *viewStub) ModelLabel() string { return "m" }
func (v *viewStub) WantsView() bool    { return v.wants }
func (v *viewStub) Review(context.Context, string, string) RawRun {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.plain++
	return RawRun{Response: "NO FINDINGS", Status: "SUCCESS"}
}
func (v *viewStub) ReviewView(_ context.Context, _, _ string, view *CommitView) RawRun {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.views = append(v.views, view)
	return RawRun{Response: "NO FINDINGS", Status: "SUCCESS"}
}

// TestPanelBuildsOneViewOnlyForAReviewerThatWantsIt: every run of a reviewer that
// reads files gets the same view of the spec; one that does not is never handed one.
func TestPanelBuildsOneViewOnlyForAReviewerThatWantsIt(t *testing.T) {
	root := committedRepo(t, map[string]string{"a.txt": "a\n"})
	cfg := Defaults()
	cfg.ReviewerRuns = 3
	wants := &viewStub{wants: true}
	NewPanel(cfg, wants).Run(context.Background(), "p", root, DiffSpec{StagedOnly: true})
	if len(wants.views) != 3 || wants.views[0] == nil || wants.views[0] != wants.views[2] || !wants.views[0].Has("a.txt") {
		t.Errorf("views = %v, want one shared view of the repository", wants.views)
	}
	off := &viewStub{}
	NewPanel(cfg, off).Run(context.Background(), "p", root, DiffSpec{StagedOnly: true})
	if len(off.views) != 0 || off.plain != 3 {
		t.Errorf("a reviewer that wants no view got %d views and %d plain runs", len(off.views), off.plain)
	}
}

// TestReviewerReportListsAPIRuns: token usage and tool use reach the row per run, and
// a panel of CLI runs writes no such field.
func TestReviewerReportListsAPIRuns(t *testing.T) {
	runs := []ReviewRun{
		Classify(RawRun{Response: "NO FINDINGS", Status: "SUCCESS",
			Usage: &TokenUsage{Prompt: 10, Completion: 2}, Tools: &ToolUse{Mode: "tools", Calls: 1}}),
		Classify(RawRun{Outcome: OutcomeToolBudget, Tools: &ToolUse{Mode: "tools", Calls: 24}}),
	}
	api, ok := ReviewerReport(Defaults(), runs)["reviewer_api_runs"].([]Row)
	if !ok || len(api) != 2 || api[0]["usage"] != (TokenUsage{Prompt: 10, Completion: 2}) ||
		api[1]["outcome"] != "tool_budget" || api[1]["tools"].(ToolUse).Calls != 24 {
		t.Errorf("reviewer_api_runs = %v", api)
	}
	cli := []ReviewRun{Classify(RawRun{Response: "NO FINDINGS", Status: "SUCCESS"})}
	if _, ok := ReviewerReport(Defaults(), cli)["reviewer_api_runs"]; ok {
		t.Error("a CLI panel wrote reviewer_api_runs")
	}
}

// TestInstrumentRecordsAnAPIToolMode: an API reviewer with tools on stamps the mode;
// tools off and a CLI reviewer stamp nothing new.
func TestInstrumentRecordsAnAPIToolMode(t *testing.T) {
	c := Defaults()
	c.ReviewerEndpoint, c.ReviewerAPITools = "openrouter.ai", "auto"
	if got := c.InstrumentFieldsWith("")["tools"]; got != "auto" {
		t.Errorf("tools = %v, want auto", got)
	}
	c.ReviewerAPITools = "off"
	if _, ok := c.InstrumentFieldsWith("")["tools"]; ok {
		t.Error("tools off stamped a tools field")
	}
	c.ReviewerEndpoint, c.ReviewerAPITools = "", "auto"
	if _, ok := c.InstrumentFieldsWith("")["tools"]; ok {
		t.Error("a CLI reviewer stamped a tools field")
	}
}
