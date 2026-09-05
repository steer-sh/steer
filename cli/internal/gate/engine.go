package gate

import (
	"context"
	"fmt"
	"time"
)

// DecisionKind is what the adapter must render. Nothing in the core knows what a
// permissionDecision is.
type DecisionKind int

const (
	// Silent emits nothing at all: the gate fell open and has no receipt to give.
	Silent DecisionKind = iota
	// Allow rules that the commit may proceed.
	Allow
	// NoteOnly tells the operator something WITHOUT ruling on the permission. The
	// chain-end paths use this rather than Allow: an allow decision skips the
	// permission prompt outright, and a gate that stops reviewing must not also start
	// auto-approving commits that would otherwise have been asked about.
	NoteOnly
	// ContextOnly gives the agent text and the operator a receipt, without ruling.
	ContextOnly
	// Deny blocks the commit and hands the agent the reviews.
	Deny
)

// Decision is the engine's whole output. Reason is the deny text or the agent-facing
// context; SystemMessage is the operator receipt.
type Decision struct {
	Kind          DecisionKind
	Reason        string
	SystemMessage string
	// LedgerError, when set, is a failure of the ledger append itself. It has no disk
	// to record on, so the adapter reports it on the operator channel.
	LedgerError error
}

// Request is one commit the engine must rule on. The adapters answer the questions
// they can answer — which tree, which diff, who the actor is — and the engine does
// the rest.
type Request struct {
	// Repo is the git toplevel the commit lands in.
	Repo string
	// Actor keys the chain: the session id under PreToolUse, HEAD under pre-commit.
	Actor string
	// Channel is ChannelPreToolUse or ChannelPreCommit, stamped on every row so a
	// reader can tell an absent field from a lost one.
	Channel string
	// SessionID names the sidecars. Empty under pre-commit by construction.
	SessionID string
	// Spec says which tree the diff is a delta against.
	Spec DiffSpec
	// Message is the commit message, best effort. Absence is not fatal — the diff
	// carries the review.
	Message string
	// RerunNote is prepended to a deny when the blocked command would itself have
	// written a file. Empty for any adapter that does not block a shell command.
	RerunNote string
	// RowExtra carries adapter-only fields onto every row this request writes.
	RowExtra Row
	// LedgerHook, when set, is called on the retry row so an adapter can attach the
	// agent's self-reported ledger before the row is written.
	LedgerHook func(row Row, state ChainState)
}

// Engine is the harness-neutral review engine.
type Engine struct {
	cfg    Config
	panel  *Panel
	ledger *Ledger
}

func NewEngine(cfg Config, reviewer Reviewer) *Engine {
	// The instrument names what ran, not what was configured.
	cfg.ReviewerProvider = reviewer.Name()
	cfg.ReviewerModel = reviewer.ModelLabel()
	return &Engine{cfg: cfg, panel: NewPanel(cfg, reviewer), ledger: NewLedger(cfg)}
}

// Config exposes the engine's configuration to the adapters.
func (e *Engine) Config() Config { return e.cfg }

// Ledger exposes the ledger so an adapter can write its own rows (ungated-partial).
func (e *Engine) Ledger() *Ledger { return e.ledger }

func (e *Engine) log(req Request, row Row) error {
	setDefault(row, "channel", req.Channel)
	// The chain key. Two chains in one repo differ by their subject and by nothing else
	// the row carries, and a session holds far more same-repo chain pairs than
	// cross-repo ones.
	if subject := CommitSubject(req.Message); subject != "" {
		setDefault(row, "subject", subject)
	}
	for key, value := range req.RowExtra {
		setDefault(row, key, value)
	}
	return e.ledger.Log(row)
}

// prepared is the reviewable material for one round.
type prepared struct {
	paths []string
	diff  string
	// notice is agent-facing text that must survive whatever this round rules. It
	// carries what the reviewers CANNOT know: which tree they were handed.
	notice string
	// raw is the unrendered diff. It is the commit's IDENTITY — the cleared record,
	// the chain state and the changed-since-last-round test all key on it — because
	// two different over-budget versions of one file render to the same stat block.
	raw string
}

// prepare resolves the diff for this round, logging the skip rows itself so both
// callers record them identically. A non-empty status means there is nothing to
// review: "norepo" (git could not answer — fail open), "prose", or "empty".
func (e *Engine) prepare(req Request, firing int, d *DiffResult) (prepared, string, error) {
	paths, ok := ChangedFiles(e.cfg, req.Repo, req.Spec)
	if !ok {
		return prepared{}, "norepo", nil
	}
	if IsProseOnly(paths) {
		err := e.log(req, Row{"ts": time.Now().Unix(), "phase": "skip-prose",
			"files": len(paths), "firing": firing})
		return prepared{}, "prose", err
	}
	if d == nil {
		got, _ := GetDiffSized(e.cfg, req.Repo, req.Spec)
		d = &got
	}
	// The RAW length decides, not the rendered one. They differ only when rendering
	// makes the diff shorter, which is exactly the over-budget branches — so testing
	// the rendered length made the smallest-commit filter discard the largest commits.
	if len(d.Raw) < e.cfg.MinDiffChars {
		// Logged, not silent. This was the only skip path writing no row, so a staging
		// bypass read as "no commit happened" rather than "a commit went ungated" —
		// the log is the experiment's data, and an invisible skip corrupts it in the
		// direction that flatters the gate.
		err := e.log(req, Row{"ts": time.Now().Unix(), "phase": "skip-empty",
			"files": len(paths), "chars": len(d.Raw), "firing": firing})
		return prepared{}, "empty", err
	}
	return prepared{paths: paths, diff: d.Text, raw: d.Raw}, "", nil
}

// review runs the panel over a prepared round.
func (e *Engine) review(ctx context.Context, req Request, p prepared) PanelResult {
	// Symbols are extracted from the RAW diff: neutralising first would leave &lt; in
	// a search term. BuildContext neutralises the files it attaches itself.
	contextBlock := BuildContext(e.cfg, req.Repo, ChangedSymbols(p.diff, p.paths), p.paths)
	prompt := BuildReviewPrompt(
		NeutralizeTags(req.Message), NeutralizeTags(p.diff), contextBlock)
	return e.panel.Run(ctx, prompt, req.Repo)
}

// Handle rules on one commit.
func (e *Engine) Handle(ctx context.Context, req Request) Decision {
	chain := NewChain(e.cfg, ChainKey{Actor: req.Actor, Repo: req.Repo})
	state := chain.Load()
	if chain.Pending(state) {
		return e.handleRetry(ctx, req, chain, state)
	}
	return e.handleFirst(ctx, req, chain)
}

func (e *Engine) handleFirst(ctx context.Context, req Request, chain *Chain) Decision {
	p, status, err := e.prepare(req, 1, nil)
	if status != "" {
		return Decision{Kind: Silent, LedgerError: err}
	}
	// The cleared record is read once the diff is in hand, because it is keyed on the
	// diff's own content. That is also one `git diff` cheaper than probing first.
	if record := chain.LoadCleared(p.raw); record != nil {
		// Logged, never silent, for the reason skip-empty is: a pass the log does not
		// carry is an invisible skip. The phase is deliberately NOT one mine.py opens
		// a chain on — a chain here is the phantom this removes.
		logErr := e.log(req, Row{"ts": time.Now().Unix(), "phase": "skip-cleared",
			"firing": 1, "diff_sha": record.DiffSHA, "chars": len(p.raw),
			"session_id": req.SessionID, "cleared_ts": record.TS,
			"cleared_channel": record.Channel})
		return Decision{
			Kind: NoteOnly,
			SystemMessage: "[commit-gate] this exact diff was reviewed and cleared by the gate " +
				"already, so it was NOT re-reviewed — this commit is a retry of one " +
				"the gate ruled on, and git rejected it for its own reasons. Commit proceeds.",
			LedgerError: logErr,
		}
	}
	return e.reviewCommit(ctx, req, chain, p, 1)
}

func (e *Engine) handleRetry(ctx context.Context, req Request, chain *Chain, state ChainState) Decision {
	// Two fields of DIFFERENT provenance, and the pair is the point: diff_changed is
	// observed from the tree and owes the agent nothing, while the ledger counts are
	// the agent's own account. Either alone is misleading — a changed diff says
	// nothing about which concerns drove it, and an accept count says nothing about
	// whether anything happened.
	after, _ := GetDiffSized(e.cfg, req.Repo, req.Spec)
	changed := after.Raw != state.Diff
	// Two observed facts, and neither is the previous round's tree: this command reads
	// the index, and edits sit unstaged. A retry reading the working tree hides nothing,
	// so only the index needs asking about.
	notice := ""
	if req.Spec.StagedOnly && HasUnstagedEdits(e.cfg, req.Repo) {
		notice = StaleIndexNotice()
	}
	firing := state.Firing
	if firing == 0 {
		firing = 1
	}
	row := Row{
		"ts": time.Now().Unix(), "phase": "retry",
		"finding_slots":   state.FindingSlots,
		"diff_changed":    changed,
		"firing":          firing,
		"spec_changed":    !state.Spec.Equal(req.Spec),
		"stale_index":     notice != "",
		"files":           state.Files,
		"session_id":      req.SessionID,
		"deny_ts":         state.TS,
		"ledger_expected": true,
	}
	if req.LedgerHook != nil {
		req.LedgerHook(row, state)
	}
	logErr := e.log(req, row)
	// Cleared unconditionally BEFORE any branch below. reviewCommit writes the next
	// round's state itself, and every other path ends the chain — so a crash between
	// here and a re-fire must not leave a stale awaiting_retry that would let the
	// following commit through unreviewed.
	if err := chain.Clear(); err != nil && logErr == nil {
		logErr = err
	}

	if !changed {
		// The chain's real stop. An all-rejected round produces this, which is why the
		// cap almost never binds.
		//
		// No cleared record while edits sit unstaged, for the reason the clean-review
		// path withholds one: the record means "reviewed and let through", and writing
		// it here blesses the very tree the notice exists to question.
		if notice == "" {
			if err := chain.RememberCleared(after.Raw, req.Channel); err != nil && logErr == nil {
				logErr = err
			}
		}
		kind := NoteOnly
		if notice != "" {
			kind = ContextOnly
		}
		return Decision{
			Kind:   kind,
			Reason: notice,
			SystemMessage: fmt.Sprintf("[commit-gate] gate chain ended after %d firing(s) — the "+
				"retry diff is byte-identical to the one already reviewed, so a re-review "+
				"would read no new code. Commit proceeds.%s", firing, separated(notice)),
			LedgerError: logErr,
		}
	}

	next := firing + 1
	p, status, err := e.prepare(req, next, &after)
	p.notice = notice
	if err != nil && logErr == nil {
		logErr = err
	}
	if firing >= e.cfg.MaxFirings {
		// The cap stops the BLOCKING chain, not the reading of the last round's fixes.
		d := e.terminalReview(ctx, req, chain, p, status, next)
		return withLedgerError(d, logErr)
	}
	if status != "" {
		if notice != "" && status == "empty" {
			// The chain goes back as it was: this pass reviewed nothing, so the round
			// it retries is still awaiting an answer.
			if err := chain.Save(state); err != nil && logErr == nil {
				logErr = err
			}
			return Decision{Kind: ContextOnly, Reason: notice,
				SystemMessage: notice, LedgerError: logErr}
		}
		return Decision{Kind: Silent, LedgerError: logErr}
	}
	// The diff changed, so this round reviews code no reviewer has read.
	return withLedgerError(e.reviewCommit(ctx, req, chain, p, next), logErr)
}

func withLedgerError(d Decision, err error) Decision {
	if d.LedgerError == nil {
		d.LedgerError = err
	}
	return d
}

// reviewCommit reviews this commit and rules on it. Called for firing 1 and for every
// re-fire, so a round 3 review is identical in depth and inputs to a round 1 review —
// the only difference is the diff it reads.
func (e *Engine) reviewCommit(ctx context.Context, req Request, chain *Chain, p prepared, firing int) Decision {
	result := e.review(ctx, req, p)

	if result.Answered == 0 {
		// Logged for the reason skip-empty is: this is a commit that went through
		// ungated, and a pass the log does not carry reads as a commit that never
		// happened. No cleared record: nothing was reviewed, so nothing is cleared.
		err := e.log(req, mergeRows(
			Row{"ts": time.Now().Unix(), "phase": "ungated", "reviewers": 0,
				"files": len(p.paths), "changed_paths": SlashPaths(p.paths),
				"firing": firing, "chars": len(p.raw), "session_id": req.SessionID},
			ReviewerReport(e.cfg, result.Runs)))
		kind := Allow
		if p.notice != "" {
			kind = ContextOnly
		}
		return Decision{
			Kind:   kind,
			Reason: p.notice,
			SystemMessage: fmt.Sprintf("[commit-gate] all %d reviewers unavailable — %s. "+
				"This commit was NOT reviewed and went through ungated.%s",
				e.cfg.ReviewerRuns, ReviewerTrouble(result.Runs), separated(p.notice)),
			LedgerError: err,
		}
	}

	if len(result.Reviews) == 0 {
		err := e.log(req, mergeRows(
			Row{"ts": time.Now().Unix(), "phase": "clean", "reviewers": result.Answered,
				"files": len(p.paths), "changed_paths": SlashPaths(p.paths),
				"firing": firing, "session_id": req.SessionID},
			ReviewerReport(e.cfg, result.Runs),
			e.ledger.SaveDiff(req.SessionID, firing, p.diff)))
		if p.notice == "" {
			// No cleared record while edits sit unstaged. The record's meaning is
			// "reviewed and let through", and a later commit presenting this same diff
			// would skip the gate on the strength of it — blessing the tree the notice
			// exists to question.
			if cerr := chain.RememberCleared(p.raw, req.Channel); cerr != nil && err == nil {
				err = cerr
			}
		}
		thin := ""
		if result.Answered < e.cfg.ReviewerRuns {
			thin = fmt.Sprintf(" Only %d/%d answered — %s.",
				result.Answered, e.cfg.ReviewerRuns, ReviewerTrouble(result.Runs))
		}
		kind := Allow
		if p.notice != "" {
			// ContextOnly rather than Allow: the notice is for the AGENT, and an allow
			// carries only the operator receipt. It costs this commit its auto-approval
			// and buys the agent the one fact the reviewers could not see.
			kind = ContextOnly
		}
		return Decision{
			Kind:   kind,
			Reason: p.notice,
			SystemMessage: fmt.Sprintf("[commit-gate] %d reviewers read the diff on firing %d "+
				"and found nothing — commit proceeds.%s%s", result.Answered, firing, thin, separated(p.notice)),
			LedgerError: err,
		}
	}

	review, findings := ComposeReview(result.Reviews)
	// `firing` rides in the state, so the retry row it produces knows which round it
	// closes.
	saveErr := chain.Save(ChainState{
		AwaitingRetry: true, TS: time.Now().Unix(), FindingSlots: findings,
		Files: len(p.paths), Diff: p.raw, Firing: firing, Spec: req.Spec,
	})
	err := e.log(req, mergeRows(
		Row{"ts": time.Now().Unix(), "phase": "deny", "reviewers": result.Answered,
			"finding_slots": findings, "files": len(p.paths),
			"changed_paths": SlashPaths(p.paths), "firing": firing,
			"session_id": req.SessionID, "severity_lines_loose": LooseFindingCount(result.Reviews),
			"reviews_off_format": OffFormatReviews(result.Reviews)},
		ReviewerReport(e.cfg, result.Runs),
		e.ledger.SaveDiff(req.SessionID, firing, p.diff),
		FindingShape(result.Reviews, p.paths)))
	if err == nil {
		err = saveErr
	}

	// The operator sees the system message; they do NOT see the deny reason, which is
	// where the reviews and the ledger instruction live. Without this line a fired gate
	// is invisible except as a slow commit, and whether they learn it fired at all
	// depends on the agent remembering to say so.
	note := fmt.Sprintf("[commit-gate] gate FIRED (round %d/%d) — %d/%d reviewers, %d raw "+
		"findings across %d file(s). The reviews went to the agent only; expect a ledger "+
		"of accept/reject verdicts in its reply.",
		firing, e.cfg.MaxFirings, result.Answered, e.cfg.ReviewerRuns, findings, len(p.paths))
	if result.Answered < e.cfg.ReviewerRuns {
		note += fmt.Sprintf(" Only %d answered, so the union is thinner than usual (%s).",
			result.Answered, ReviewerTrouble(result.Runs))
	}
	return Decision{
		Kind:          Deny,
		Reason:        p.notice + BuildDeny(result.Answered, review, firing, e.cfg.MaxFirings, req.RerunNote, CommitSubject(req.Message), e.cfg.ReviewerLabel()),
		SystemMessage: note,
		LedgerError:   err,
	}
}

// terminalReview reviews the capped chain's last fixes without ruling on this commit.
// The commit keeps its normal permission path while the findings reach the agent as
// context and the operator gets the count.
func (e *Engine) terminalReview(ctx context.Context, req Request, chain *Chain, p prepared, status string, firing int) Decision {
	capped := fmt.Sprintf("[commit-gate] gate chain hit the %d-firing cap", e.cfg.MaxFirings)
	if status != "" {
		kind := NoteOnly
		if p.notice != "" {
			// NoteOnly speaks to the operator alone, so a Reason set beside it never
			// reaches the agent that has to act on it.
			kind = ContextOnly
		}
		return Decision{Kind: kind, Reason: p.notice, SystemMessage: fmt.Sprintf(
			"%s. The final review did not run (%s), so round %d's fixes are UNREVIEWED.%s",
			capped, status, e.cfg.MaxFirings, separated(p.notice))}
	}
	result := e.review(ctx, req, p)
	row := mergeRows(
		Row{"ts": time.Now().Unix(), "phase": "cap", "firing": firing,
			"reviewers": result.Answered, "files": len(p.paths),
			"changed_paths": SlashPaths(p.paths), "session_id": req.SessionID},
		ReviewerReport(e.cfg, result.Runs),
		e.ledger.SaveDiff(req.SessionID, firing, p.diff))

	if result.Answered == 0 {
		err := e.log(req, row)
		kind := NoteOnly
		if p.notice != "" {
			kind = ContextOnly
		}
		return Decision{Kind: kind, Reason: p.notice, SystemMessage: fmt.Sprintf(
			"%s and every reviewer was unavailable for the final pass, so round %d's "+
				"fixes are UNREVIEWED — %s.%s", capped, e.cfg.MaxFirings,
			ReviewerTrouble(result.Runs), separated(p.notice)),
			LedgerError: err}
	}

	// Below this line a reviewer read the diff and the commit proceeds, which is what
	// the cleared record means. The branch above reviewed nothing and must not write it,
	// and neither does a pass carrying the stale-index notice: the record would bless
	// the tree the notice questions.
	var clearedErr error
	if p.notice == "" {
		clearedErr = chain.RememberCleared(p.raw, req.Channel)
	}

	if len(result.Reviews) == 0 {
		row["finding_slots"] = 0
		err := e.log(req, row)
		if err == nil {
			err = clearedErr
		}
		kind := NoteOnly
		if p.notice != "" {
			kind = ContextOnly
		}
		return Decision{Kind: kind, Reason: p.notice, SystemMessage: fmt.Sprintf(
			"%s. The final non-blocking review read round %d's fixes and %d reviewers "+
				"found nothing.%s", capped, e.cfg.MaxFirings, result.Answered, separated(p.notice)),
			LedgerError: err}
	}

	review, findings := ComposeReview(result.Reviews)
	shape := FindingShape(result.Reviews, p.paths)
	row["finding_slots"] = findings
	row["severity_lines_loose"] = LooseFindingCount(result.Reviews)
	row["reviews_off_format"] = OffFormatReviews(result.Reviews)
	err := e.log(req, mergeRows(row, shape))
	if err == nil {
		err = clearedErr
	}

	blocking := 0
	if severities, ok := shape["severities"].(map[string]int); ok {
		blocking = severities["BLOCKING"]
	}
	note := fmt.Sprintf("%s — final NON-BLOCKING review of round %d's fixes: %d raw findings "+
		"(%d BLOCKING) from %d reviewers. This commit is NOT blocked; expect a ledger, and "+
		"a follow-up commit if anything blocking survives.",
		capped, e.cfg.MaxFirings, findings, blocking, result.Answered)
	return Decision{
		Kind:          ContextOnly,
		Reason:        p.notice + BuildCap(result.Answered, review, firing, e.cfg.MaxFirings, CommitSubject(req.Message), e.cfg.ReviewerLabel()),
		SystemMessage: note,
		LedgerError:   err,
	}
}

func mergeRows(rows ...Row) Row {
	out := Row{}
	for _, row := range rows {
		for key, value := range row {
			out[key] = value
		}
	}
	return out
}

// separated puts a break before a notice appended to an operator line, and stays empty
// when there is no notice to append.
func separated(notice string) string {
	if notice == "" {
		return ""
	}
	return "\n\n" + notice
}
