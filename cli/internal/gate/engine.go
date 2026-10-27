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
	// ContextOnly gives the agent text and the operator a receipt, without ruling. The
	// chain-end paths use this rather than Allow: an allow decision skips the
	// permission prompt outright, and a gate that stops reviewing must not also start
	// auto-approving commits that would otherwise have been asked about.
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
	// Receipt is true when Reason is the agent's copy of the SystemMessage receipt, under
	// the commit's full identity. A channel whose agent and operator read one stream
	// writes only Reason.
	Receipt bool
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
	// TurnKey names the payload field that identifies the agent's turn: "prompt_id" or
	// "turn_id". Empty for a channel that has no turns.
	TurnKey string
	// TurnID is that field's value, empty when the payload carried none.
	TurnID string
	// ThreadID names the child thread the commit came from, empty on the root thread.
	ThreadID string

	// addendum is loaded once by Handle, and every row and prompt of the request reads it.
	addendum Addendum
	// chainID is the chain this request reviews in: new on a first review, carried
	// from the chain state on a retry.
	chainID string
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
	cfg.ReviewerEndpoint = EndpointOf(reviewer)
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
	if req.chainID != "" {
		setDefault(row, "chain_id", req.chainID)
	}
	for key, value := range req.RowExtra {
		setDefault(row, key, value)
	}
	digest := req.addendum.Digest()
	setDefault(row, "instrument", e.cfg.InstrumentIDWith(digest))
	setDefault(row, "instrument_fields", e.cfg.InstrumentFieldsWith(digest))
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
// review: "norepo" (git could not answer — fail open), "prose", "binary", or "empty".
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
	if d.Complete && IsBinaryOnly(d.Raw, paths) && !externalDiffConfigured(req.Repo) {
		err := e.log(req, Row{"ts": time.Now().Unix(), "phase": "skip-binary",
			"files": len(paths), "firing": firing})
		return prepared{}, "binary", err
	}
	// The RAW diff decides, not the rendered one: rendering adds notes and shortens an
	// over-budget diff. Any non-empty diff is reviewed, whatever its size.
	if d.Raw == "" {
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
	// Symbols come from the budgeted diff before neutralisation: neutralising first
	// would leave &lt; in a search term, and a dropped file's hunks seed no search.
	// Changed-path stems stay search terms. BuildContext neutralises its own files.
	contextBlock := BuildContext(e.cfg, req.Repo, ChangedSymbols(p.diff, p.paths), p.paths)
	prompt := BuildReviewPrompt(
		NeutralizeTags(req.Message), NeutralizeTags(p.diff), contextBlock, req.addendum)
	return e.panel.Run(ctx, prompt, req.Repo, req.Spec)
}

// Handle rules on one commit.
func (e *Engine) Handle(ctx context.Context, req Request) Decision {
	req.addendum = LoadAddendum(req.Repo)
	for _, msg := range req.addendum.Errors {
		e.ledger.noteErr("%s", msg)
	}
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
		return e.skipped(req, status, err)
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
			"cleared_channel": record.Channel, "cleared_ending": record.Ending})
		return proceed(ContextOnly, req, p.raw, 1,
			"NOT reviewed again — "+clearedEnding(record.Ending)+", and git "+
				"refused that commit for its own reasons. The gate does not block it.",
			logErr)
	}
	req.chainID = NewChainID()
	return e.reviewCommit(ctx, req, chain, p, 1)
}

// clearedEnding words how the earlier review sequence let this exact diff through.
func clearedEnding(ending string) string {
	switch ending {
	case ClearedClean:
		return "reviewers already passed this exact diff"
	case ClearedUnchanged:
		return "the review sequence already ended on this exact diff, after reviewers " +
			"blocked it and it came back unchanged"
	case ClearedCap:
		return "the review sequence already ended on this exact diff at its block limit"
	default:
		return "a review sequence already ended on this exact diff"
	}
}

func (e *Engine) handleRetry(ctx context.Context, req Request, chain *Chain, state ChainState) Decision {
	req.chainID = state.ChainID
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
			if err := chain.RememberCleared(after.Raw, req.Channel, ClearedUnchanged); err != nil && logErr == nil {
				logErr = err
			}
		}
		return e.release(proceed(ContextOnly, req, after.Raw, firing, fmt.Sprintf(
			"the review sequence ended after %d block(s) — this diff is identical to the "+
				"one the reviewers already read, so a new review would read no new code. The "+
				"gate does not block it.%s", firing, separated(notice)), logErr), req, firing, 0)
	}

	next := firing + 1
	p, status, err := e.prepare(req, next, &after)
	p.notice = notice
	if err != nil && logErr == nil {
		logErr = err
	}
	if firing >= e.cfg.MaxFirings {
		// The cap stops the BLOCKING chain, not the reading of the last round's fixes.
		d, terminal := e.terminalReview(ctx, req, chain, p, status, next)
		return e.release(withLedgerError(d, logErr), req, firing, terminal)
	}
	if status != "" {
		if notice != "" && status == "empty" {
			// The chain goes back as it was: this pass reviewed nothing, so the round
			// it retries is still awaiting an answer.
			if err := chain.Save(state); err != nil && logErr == nil {
				logErr = err
			}
			// The identity is the DENIED round's, both halves. This pass read an empty
			// diff, so its own digest names nothing, and the notice is
			// about the round the chain still awaits an answer for.
			return proceed(ContextOnly, req, state.Diff, firing, notice, logErr)
		}
		return e.release(e.skipped(req, status, logErr), req, firing, 0)
	}
	// The diff changed, so this round reviews code no reviewer has read.
	return e.release(withLedgerError(e.reviewCommit(ctx, req, chain, p, next), logErr), req, firing, 0)
}

// release finishes a decision that ends a sequence which blocked `blocks` times. A
// receipt gets the ledger instruction; the cap's terminal review carries it in its own
// template and passes the round it names as terminal. Either way the release row records
// the ledger the agent now owes. A deny continues the sequence and a silent decision
// rules nothing, so neither is a release.
func (e *Engine) release(d Decision, req Request, blocks, terminal int) Decision {
	if d.Kind == Deny || d.Kind == Silent || (!d.Receipt && terminal == 0) {
		return d
	}
	header := ledgerHeaderLine(req)
	if d.Receipt {
		d.Reason += fmt.Sprintf("\n\nThis commit ends the review sequence, so its ledger is due NOW: "+
			"write it as text in this reply, before your next tool call, with one section for "+
			"each of the %d round(s) that blocked. Copy this header line as it stands:\n\n%s",
			blocks, header)
	}
	row := Row{"ts": time.Now().Unix(), "phase": PhaseChainEnd, "blocks": blocks,
		"ledger_header": header, "session_id": req.SessionID, "actor": req.Actor,
		"release_thread": req.ThreadID, "release_turn_key": req.TurnKey,
		"release_turn": req.TurnID, "release_turn_present": req.TurnID != ""}
	if terminal > 0 {
		row["terminal_round"] = terminal
	}
	if _, ok := req.RowExtra["repo"]; !ok {
		row["repo"] = req.Repo
	}
	if err := e.log(req, row); err != nil && d.LedgerError == nil {
		d.LedgerError = err
	}
	if req.TurnKey != "" && req.TurnID == "" {
		d.SystemMessage = joinNote(d.SystemMessage, "Ledger backstop unavailable for this sequence: "+
			"PreToolUse supplied no usable "+req.TurnKey+".")
	}
	return d
}

// ledgerHeaderLine is the header line the agent is told to copy for this request's
// sequence, in the form the deny and cap templates render.
func ledgerHeaderLine(req Request) string {
	subject := CommitSubject(req.Message)
	if subject == "" {
		subject = NoSubject
	}
	return fmt.Sprintf("**%s — review sequence%s, %s**", LedgerHeader, chainSlot(req.chainID), subject)
}

// joinNote appends an operator note to a system message.
func joinNote(message, note string) string {
	if message == "" {
		return note
	}
	return message + " " + note
}

// skipped answers a commit the gate chose not to review, with one receipt that names
// the reason on both channels: a skip otherwise looks exactly like a gate that never
// ran. It rules nothing, so the commit keeps its own permission path. A repository git
// could not read is the fail-open case and stays silent.
func (e *Engine) skipped(req Request, status string, err error) Decision {
	var why string
	switch {
	case status == "prose":
		why = "every changed file is prose (.md, .txt and the like), which the gate does not review"
	case status == "binary":
		why = "only the content of existing binary files changed, with paths and modes " +
			"unchanged, and a reviewer cannot read binary content"
	case status == "empty":
		why = "the diff is empty"
	default:
		return Decision{Kind: Silent, LedgerError: err}
	}
	text := fmt.Sprintf("[commit-gate] %s: not reviewed — %s.", CommitName(req), why)
	return Decision{Kind: ContextOnly, Reason: text, SystemMessage: text, Receipt: true, LedgerError: err}
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
		return proceed(kind, req, p.raw, firing, fmt.Sprintf(
			"all %d reviewers unavailable — %s. This commit was NOT reviewed; it "+
				"proceeds with no review.%s", e.cfg.ReviewerRuns, ReviewerTrouble(result.Runs),
			separated(p.notice)), err)
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
			if cerr := chain.RememberCleared(p.raw, req.Channel, ClearedClean); cerr != nil && err == nil {
				err = cerr
			}
		}
		thin := ""
		if result.Answered < e.cfg.ReviewerRuns {
			thin = fmt.Sprintf(" Only %d of %d reviewers answered — %s.",
				result.Answered, e.cfg.ReviewerRuns, ReviewerTrouble(result.Runs))
		}
		// A notice means the reviewers may have read a tree the commit will not land, so
		// the commit keeps its permission prompt. Both kinds carry the receipt.
		kind := Allow
		if p.notice != "" {
			kind = ContextOnly
		}
		found := fmt.Sprintf("%d reviewers read this diff and found nothing — commit proceeds.",
			result.Answered)
		tail := thin + separated(p.notice)
		d := proceed(kind, req, p.raw, firing, found+tail, err)
		d.Reason = agentReceipt(req, p.raw, firing,
			found+" The review ran; that is not a claim the change is correct."+tail)
		return d
	}

	review, findings := ComposeReview(result.Reviews)
	// `firing` rides in the state, so the retry row it produces knows which round it
	// closes.
	saveErr := chain.Save(ChainState{
		AwaitingRetry: true, TS: time.Now().Unix(), FindingSlots: findings,
		Files: len(p.paths), Diff: p.raw, Firing: firing, Spec: req.Spec,
		ChainID: req.chainID,
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
	note := fmt.Sprintf("[commit-gate] Commit blocked — %d of %d reviewers returned %d "+
		"finding(s), duplicates included, on a diff of %d changed file(s). Only the agent sees "+
		"the findings; expect its answer to each one in its reply. Blocking round %d of at most %d.",
		result.Answered, e.cfg.ReviewerRuns, findings, len(p.paths), firing, e.cfg.MaxFirings)
	if result.Answered < e.cfg.ReviewerRuns {
		note += fmt.Sprintf(" The other %d did not answer (%s).",
			e.cfg.ReviewerRuns-result.Answered, ReviewerTrouble(result.Runs))
	}
	return Decision{
		Kind: Deny,
		Reason: p.notice + BuildDeny(result.Answered, review, firing, e.cfg.MaxFirings, req.RerunNote, CommitSubject(req.Message), req.chainID, e.cfg.ReviewerLabel()) +
			separated(pendingProjectAddendumNotice(req.Repo, req.addendum, p.paths)),
		SystemMessage: note,
		LedgerError:   err,
	}
}

// terminalReview reviews the capped chain's last fixes without ruling on this commit.
// The commit keeps its normal permission path while the findings reach the agent as
// context and the operator gets the count. terminal is the round the agent's ledger
// must answer as terminal: firing when the findings reached the agent, else 0.
func (e *Engine) terminalReview(ctx context.Context, req Request, chain *Chain, p prepared, status string, firing int) (d Decision, terminal int) {
	capped := fmt.Sprintf("the review sequence reached its limit of %d blocks", e.cfg.MaxFirings)
	if status != "" {
		return proceed(ContextOnly, req, p.raw, firing, fmt.Sprintf(
			"%s. The final review did not run (%s), so round %d's fixes are UNREVIEWED.%s",
			capped, status, e.cfg.MaxFirings, separated(p.notice)), nil), 0
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
		return proceed(ContextOnly, req, p.raw, firing, fmt.Sprintf(
			"%s and every reviewer was unavailable for the final pass, so round %d's "+
				"fixes are UNREVIEWED — %s.%s", capped, e.cfg.MaxFirings,
			ReviewerTrouble(result.Runs), separated(p.notice)), err), 0
	}

	// Below this line a reviewer read the diff and the commit proceeds, which is what
	// the cleared record means. The branch above reviewed nothing and must not write it,
	// and neither does a pass carrying the stale-index notice: the record would bless
	// the tree the notice questions.
	var clearedErr error
	if p.notice == "" {
		clearedErr = chain.RememberCleared(p.raw, req.Channel, ClearedCap)
	}

	if len(result.Reviews) == 0 {
		row["finding_slots"] = 0
		err := e.log(req, row)
		if err == nil {
			err = clearedErr
		}
		return proceed(ContextOnly, req, p.raw, firing, fmt.Sprintf(
			"%s. The final non-blocking review read round %d's fixes and %d reviewers "+
				"found nothing.%s", capped, e.cfg.MaxFirings, result.Answered,
			separated(p.notice)), err), 0
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
	note := fmt.Sprintf("[commit-gate] Commit not blocked — %s. A final review of round %d's "+
		"fixes returned %d finding(s), duplicates included (%d BLOCKING), from %d reviewers. "+
		"Expect the agent's answer to each one, and a follow-up commit if a blocking finding holds.",
		capped, e.cfg.MaxFirings, findings, blocking, result.Answered)
	return Decision{
		Kind:          ContextOnly,
		Reason:        p.notice + BuildCap(result.Answered, review, firing, e.cfg.MaxFirings, CommitSubject(req.Message), req.chainID, e.cfg.ReviewerLabel()),
		SystemMessage: note,
		LedgerError:   err,
	}, firing
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
