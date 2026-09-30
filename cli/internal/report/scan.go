package report

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/shellsyntax"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

const (
	kindCorrection = "user_corrections"
	kindRejection  = "user_rejections"
	kindFailure    = "tool_failures"
	kindHookBlock  = "hook_blocks"

	kindRepeatedWording = "repeated_wording"
	kindAfterInterrupt  = "after_interrupt"

	// tool_result payloads can be huge (full test runs); only the head is
	// needed to classify them.
	maxResultScan = 4096

	// rejectionEnvelope opens the error result Claude Code writes for a tool call
	// the user declined at the permission prompt, lowercased.
	rejectionEnvelope = "the user doesn't want to proceed with this tool use"

	// maxRepeatRunes bounds the turns repeated wording collects. It is a
	// retrieval heuristic: standing instructions are short, and long turns are
	// briefs that rarely recur verbatim.
	maxRepeatRunes = 400
	// minRepeatSessions is how many sessions a wording must occur in.
	minRepeatSessions = 2
	// sendGroupWindow bounds a send group from its earliest member: identical
	// text sent to other sessions within it counts as one send.
	sendGroupWindow = 10 * time.Minute

	// maxObservedLen bounds the context text a correction occurrence carries.
	maxObservedLen = 400
	// maxRecentToolCalls bounds the tool calls an occurrence carries as context.
	maxRecentToolCalls = 3
	// maxToolTargetLen bounds each context tool call's target.
	maxToolTargetLen = 96
)

// correctionPatterns match user-turn language that can name a correction. A match
// is a heuristic correction candidate, not a labelled correction: precision is
// checked by hand and recall is unmeasured.
var correctionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(don'?t|do not|stop|quit|never) (use|using|add|adding|write|writing|create|creating|put|putting|make|making|do|doing|hardcode|hardcoding)\b`),
	regexp.MustCompile(`(?i)\bi (said|told you|asked you|already (said|told)( you)?)[,:]? (not to \w+|to (use|stop|avoid)|use|stop|avoid|don'?t|do not|never)\b`),
	regexp.MustCompile(`(?i)\buse \S{1,40} instead\b`),
	regexp.MustCompile(`(?i)\bwhy (are|do|did|would) you\b`),
	regexp.MustCompile(`(?i)\b(again|still) (using|doing|adding|writing)\b`),
	regexp.MustCompile(`(?i)\byou (keep|always|still) (us|do|add|writ|creat|mak|forget)`),
}

// askedOpinion matches a "why do you think" question at a correction match's start.
var askedOpinion = regexp.MustCompile(`(?i)^why (are|do|did|would) you think\b`)

// pastedBlock matches text the harness marks as pasted into a turn, through its
// closing tag or, when the tag was never closed, to the end of the turn.
var pastedBlock = regexp.MustCompile(`(?s)<pasted_content\b[^>]*>(?:.*?</pasted_content>|.*$)`)

// steerRuleLine matches a line of Steer's own rule output quoted back into a turn:
// the section header or a "- rule | instead" bullet.
var steerRuleLine = regexp.MustCompile(`^(## Steer Rules$|- .+ \| .+)`)

// failurePattern matches lint/test/compile failures in Bash tool output.
var failurePattern = regexp.MustCompile(`(?i)(error TS\d+|\beslint\b|\bFAILED\b|\bFAIL:|AssertionError|SyntaxError|Traceback \(most recent call last\)|panic:|\bruff\b|\bflake8\b|\bmypy\b|npm ERR!|ERROR in |error\[E\d+\])`)

var whitespaceRun = regexp.MustCompile(`\s+`)

// toolCall is what the scanner remembers about a tool call so the later result
// can be attributed.
type toolCall struct {
	Name   string
	Target string
	Shell  bool
}

// Classifier turns one session's normalized events into friction signals. It is the
// one classifier the digest and the session-snapshot store share, so a class regex
// authored against one reads the same text in the other. It is stateful — a tool
// result is attributed through the call it answers — so one Classifier serves one
// session.
type Classifier struct {
	calls map[string]toolCall
}

// NewClassifier returns a classifier for one session.
func NewClassifier() *Classifier { return &Classifier{calls: map[string]toolCall{}} }

// Signal is one classified friction event. Key is the text a class regex is matched
// against; Example is the longer form a digest shows.
type Signal struct {
	Kind    string
	Key     string
	Example string
}

// Classify records a tool call, or classifies a human turn or a tool result. The bool
// is false when the event carries no signal.
func (c *Classifier) Classify(e transcript.Event) (Signal, bool) {
	switch e.Kind {
	case transcript.KindToolCall:
		c.calls[e.ToolUseID] = toolCall{Name: e.Tool, Target: e.Target, Shell: e.Shell}
	case transcript.KindHumanTurn:
		if !e.Meta {
			return c.correction(e.Text)
		}
	case transcript.KindToolResult:
		return c.toolResult(e)
	}
	return Signal{}, false
}

// correction matches a human turn against correction candidate language. Pasted
// blocks are removed first: they are text the person brought in, not what they said.
func (c *Classifier) correction(text string) (Signal, bool) {
	trimmed, ok := typedText(text)
	if !ok {
		return Signal{}, false
	}
	for _, p := range correctionPatterns {
		for _, loc := range p.FindAllStringIndex(trimmed, -1) {
			snippet := matchLine(trimmed, loc[0])
			if steerRuleLine.MatchString(snippet) || askedOpinion.MatchString(trimmed[loc[0]:]) {
				continue
			}
			return Signal{kindCorrection, snippet, snippet}, true
		}
	}
	return Signal{}, false
}

// typedText is a human turn without its pasted blocks. It is false when nothing
// remains, or when the turn is a harness wrapper or banner.
func typedText(text string) (string, bool) {
	trimmed := strings.TrimSpace(pastedBlock.ReplaceAllString(text, ""))
	if trimmed == "" || strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "Caveat:") {
		return "", false
	}
	return trimmed, true
}

// toolResult classifies a result as rejection, hook block, or lint/test failure.
func (c *Classifier) toolResult(e transcript.Event) (Signal, bool) {
	text := e.Text
	if text == "" {
		return Signal{}, false
	}
	if len(text) > maxResultScan {
		text = text[:maxResultScan]
	}
	call := c.calls[e.ToolUseID]
	lower := strings.ToLower(text)

	switch {
	case e.IsError && declined(lower):
		key := call.Name + ": " + call.Target
		return Signal{kindRejection, key, key}, true
	case e.IsError && isHookBlock(lower):
		snippet := hookBlockSnippet(text, lower)
		return Signal{kindHookBlock, snippet, snippet}, true
	// Only a command that RUNS a program can report a lint, test or compile
	// failure of its own. A reader prints somebody else's text, and a shell call
	// whose command was not recorded cannot be told apart from one.
	case call.Shell && shellRunsProgram(call.Target):
		if pos, ok := failureMatch(text); ok {
			snippet := matchLine(text, pos)
			// A traceback opener names no cause, so every Python crash keys to
			// the one word and no class regex can tell them apart. The
			// exception the traceback ends on is the discriminating line.
			if strings.HasPrefix(snippet, tracebackOpener) {
				// From the matched line, never from the whole result: a later
				// unrelated crash in the same output is not this signal's cause.
				from := strings.LastIndexByte(text[:pos], '\n') + 1
				if exc := tracebackException(text[from:]); exc != "" {
					snippet = exc
				}
			}
			return Signal{kindFailure, snippet, snippet + " — cmd: " + call.Target}, true
		}
	}
	return Signal{}, false
}

// declined reports whether a tool result's text opens with Claude Code's decline
// envelope. The caller checks that the result is an error.
func declined(text string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), rejectionEnvelope)
}

// steerFooter is the marker every steer guard appends to its deny reason. Only
// Claude Code and Codex wrap that reason in a sentence of their own carrying
// "blocked" or "denied"; Pi passes it through untouched.
const steerFooter = "steer hook: "

func isHookBlock(lower string) bool {
	if strings.Contains(lower, steerFooter) {
		return true
	}
	return strings.Contains(lower, "hook") &&
		(strings.Contains(lower, "block") || strings.Contains(lower, "denied"))
}

// hookBlockSnippet keys on the guard's own footer where there is one. A
// harness's wrapper sentence is the same for every guard, so keying on that
// collapses each harness's blocks into one cluster naming no cause.
func hookBlockSnippet(text, lower string) string {
	i := strings.Index(lower, steerFooter)
	if i < 0 {
		return matchLine(text, strings.Index(lower, "hook"))
	}
	// The footer's first line continues with the firing identifier, and Codex
	// appends the blocked command to the footer. Both vary per call, so the key
	// stops at the name.
	line := matchLine(text, i)
	if cut := strings.Index(line, " · "); cut >= 0 {
		line = line[:cut]
	}
	return line
}

// ParserInstrument identifies the classifier that produced a set of signals. A record
// stores it at registration, so a later change to these patterns reads as instrument
// drift rather than as an effect of the intervention.
func ParserInstrument() string {
	h := sha256.New()
	h.Write([]byte(shellsyntax.Instrument))
	h.Write([]byte{0})
	for _, p := range correctionPatterns {
		h.Write([]byte(p.String()))
		h.Write([]byte{0})
	}
	h.Write([]byte("correction-skip:" + steerRuleLine.String()))
	h.Write([]byte{0})
	h.Write([]byte("correction-skip:" + askedOpinion.String()))
	h.Write([]byte{0})
	h.Write([]byte("correction-skip:" + pastedBlock.String()))
	h.Write([]byte{0})
	h.Write([]byte(failurePattern.String()))
	h.Write([]byte{0})
	h.Write([]byte("failure:runner-command+unquoted-match+traceback-exception-by-indent"))
	h.Write([]byte{0})
	for _, v := range sortedRunnerVerbs() {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	h.Write([]byte("rejection:is-error+prefix:" + rejectionEnvelope))
	h.Write([]byte{0})
	h.Write([]byte("hookblock:steer-footer-to-name|hook+block|denied,key-on-footer"))
	return "scan-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// Scanner accumulates signal clusters from normalized transcript events. It
// reads no harness format of its own: an Adapter supplies the events.
type Scanner struct {
	Since      time.Time
	stats      Stats
	clusters   map[string]map[string]*Cluster
	sessions   map[string]struct{}
	threads    map[string]struct{}
	coverage   map[harness.ID]*harnessScan
	wording    map[string]*textGroup
	interrupts map[string]*textGroup
}

// textGroup is one normalized human text and every place it was typed.
type textGroup struct {
	occurrences []Occurrence
	sessions    map[string]struct{}
}

// threadContext is what one session's thread showed before its current event:
// its human text, whether the person just stopped a running turn, and the tool
// calls requested since the previous real human turn.
type threadContext struct {
	first, previous *ObservedText
	afterInterrupt  bool
	calls           []*RecentToolCall
	callsByID       map[string]*RecentToolCall
	omitted         int
}

// addCall keeps a requested tool call in the bounded tail.
func (ctx *threadContext) addCall(e transcript.Event) {
	target := clip(e.Target, maxToolTargetLen)
	call := &RecentToolCall{Tool: e.Tool, Target: target, Cut: len(target) < len(e.Target), Result: toolResultUnobserved}
	if len(ctx.calls) == maxRecentToolCalls {
		for id, c := range ctx.callsByID {
			if c == ctx.calls[0] {
				delete(ctx.callsByID, id)
			}
		}
		ctx.calls = ctx.calls[1:]
		ctx.omitted++
	}
	ctx.calls = append(ctx.calls, call)
	if e.ToolUseID != "" {
		if ctx.callsByID == nil {
			ctx.callsByID = map[string]*RecentToolCall{}
		}
		ctx.callsByID[e.ToolUseID] = call
	}
}

// addResult records a result on the kept call it answers.
func (ctx *threadContext) addResult(e transcript.Event) {
	call := ctx.callsByID[e.ToolUseID]
	if call == nil || e.ToolUseID == "" {
		return
	}
	switch {
	case !e.IsError:
		call.Result = toolResultNonError
	case declined(e.Text):
		call.Result = toolResultDeclined
	default:
		call.Result = toolResultError
	}
}

// recentCalls copies the tail as it stands, so a later result cannot change
// it. It is nil when no call was observed.
func (ctx *threadContext) recentCalls() *RecentToolCalls {
	if len(ctx.calls) == 0 {
		return nil
	}
	out := &RecentToolCalls{Calls: make([]RecentToolCall, len(ctx.calls)), Omitted: ctx.omitted}
	for i, c := range ctx.calls {
		out.Calls[i] = *c
	}
	return out
}

// resetCalls opens the interval that the next real human turn closes.
func (ctx *threadContext) resetCalls() {
	ctx.calls, ctx.callsByID, ctx.omitted = nil, nil, 0
}

// harnessScan is one harness's running coverage. It keeps its own id sets: the
// scanner-wide sets answer "how many sessions in all", which is not the sum of
// the per-harness counts if two harnesses ever mint the same id.
type harnessScan struct {
	cov      HarnessCoverage
	sessions map[string]struct{}
	threads  map[string]struct{}
}

// NewScanner returns a Scanner that ignores entries older than since.
func NewScanner(since time.Time) *Scanner {
	return &Scanner{
		Since: since,
		clusters: map[string]map[string]*Cluster{
			kindCorrection: {},
			kindRejection:  {},
			kindFailure:    {},
			kindHookBlock:  {},
		},
		sessions:   map[string]struct{}{},
		threads:    map[string]struct{}{},
		coverage:   map[harness.ID]*harnessScan{},
		wording:    map[string]*textGroup{},
		interrupts: map[string]*textGroup{},
	}
}

// ScanSession streams one session through the adapter and classifies its events.
func (s *Scanner) ScanSession(a transcript.Adapter, session transcript.Session) error {
	// One classifier per session: a tool result is attributed through the call it
	// answers, and results always land in the same transcript as their call.
	c := NewClassifier()
	hs := s.harnessScanFor(session.Harness)
	contexts := map[string]*threadContext{}

	counts, err := a.Stream(session, transcript.Filter{Since: s.Since}, func(e transcript.Event) error {
		s.classify(e, c, hs, contexts)
		return nil
	})
	s.stats.FilesScanned++
	s.stats.BytesScanned += counts.Bytes
	s.stats.EntriesScanned += counts.Entries
	s.stats.EntriesSkipped += counts.Skipped
	hs.cov.FilesScanned++
	hs.cov.BytesScanned += counts.Bytes
	hs.cov.EntriesScanned += counts.Entries
	hs.cov.EntriesSkipped += counts.Skipped
	return err
}

// Reached opens a harness's coverage row, and is what separates a harness that
// scanned clean from one the scan never got to: a caller must call it for every
// harness whose transcripts it could read, whether or not any session followed.
// A row of zeros then means no evidence; no row at all means unreachable.
func (s *Scanner) Reached(id harness.ID) { s.harnessScanFor(id) }

func (s *Scanner) harnessScanFor(id harness.ID) *harnessScan {
	hs, ok := s.coverage[id]
	if !ok {
		hs = &harnessScan{
			cov:      HarnessCoverage{Harness: id},
			sessions: map[string]struct{}{},
			threads:  map[string]struct{}{},
		}
		s.coverage[id] = hs
	}
	return hs
}

func (s *Scanner) classify(e transcript.Event, c *Classifier, hs *harnessScan, contexts map[string]*threadContext) {
	if e.SessionID != "" {
		if _, seen := hs.sessions[e.SessionID]; !seen {
			hs.sessions[e.SessionID] = struct{}{}
			hs.cov.SessionsScanned++
		}
		s.sessions[e.SessionID] = struct{}{}
	}
	if e.ThreadID != "" {
		if _, seen := hs.threads[e.ThreadID]; !seen {
			hs.threads[e.ThreadID] = struct{}{}
			hs.cov.ThreadsScanned++
		}
		s.threads[e.ThreadID] = struct{}{}
	}
	key := e.SessionID + "\x00" + e.ThreadID
	ctx := contexts[key]
	if ctx == nil {
		ctx = &threadContext{}
		contexts[key] = ctx
	}
	if e.Interrupt != "" {
		// A declined tool call is already a rejection, so only a stopped turn
		// makes the next message an after-interrupt one.
		ctx.afterInterrupt = e.Interrupt == transcript.InterruptTurn
	}
	switch e.Kind {
	case transcript.KindToolCall:
		ctx.addCall(e)
	case transcript.KindToolResult:
		ctx.addResult(e)
	}
	var observed *ObservedText
	var recent *RecentToolCalls
	real := false
	if e.Kind == transcript.KindHumanTurn && !e.Meta {
		observed, real = observe(e.Text)
	}
	if real && !e.DeferredHumanTurn {
		recent = ctx.recentCalls()
	}
	// The next real message answers the interrupt, whatever it says.
	answers := real && ctx.afterInterrupt
	if real {
		ctx.afterInterrupt = false
	}
	corrected := false
	if sig, ok := c.Classify(e); ok {
		o := s.addCluster(sig.Kind, sig.Key, sig.Example, e)
		if o != nil && sig.Kind == kindCorrection {
			corrected = true
			o.PreviousHumanText = ctx.previous
			if ctx.first != ctx.previous {
				o.FirstObservedHumanTextInScan = ctx.first
			}
			o.AfterInterrupt = answers
			o.RecentToolCalls = recent
		}
	}
	if !real {
		return
	}
	if answers && !corrected {
		s.addAfterInterrupt(e, ctx, recent)
	}
	ctx.resetCalls()
	if ctx.first == nil {
		ctx.first = observed
	}
	ctx.previous = observed
	if e.ThreadID == "" {
		s.addWording(e)
	}
}

// pastedPlaceholder stands in context text for a pasted block, so a turn that
// held only a paste still reads as the previous turn.
const pastedPlaceholder = "[pasted text]"

// observe renders a human turn as context: pasted blocks become a placeholder,
// whitespace collapses, and the text is cut to maxObservedLen. It is false for
// an empty turn and for a harness wrapper or banner.
func observe(text string) (*ObservedText, bool) {
	trimmed := strings.TrimSpace(pastedBlock.ReplaceAllString(text, pastedPlaceholder))
	if trimmed == "" || strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "Caveat:") {
		return nil, false
	}
	flat := whitespaceRun.ReplaceAllString(trimmed, " ")
	cut := clip(flat, maxObservedLen)
	return &ObservedText{Text: cut, Cut: len(cut) < len(flat)}, true
}

// addWording records a root-thread human turn's typed text for repeated
// wording. The text is matched whole after whitespace normalization only, so
// two sentences that differ in punctuation stay apart.
func (s *Scanner) addWording(e transcript.Event) {
	typed, ok := typedText(e.Text)
	if !ok {
		return
	}
	text := whitespaceRun.ReplaceAllString(typed, " ")
	if utf8.RuneCountInString(text) > maxRepeatRunes {
		return
	}
	addText(s.wording, text, Occurrence{Session: e.SessionID, Timestamp: e.Timestamp, Text: text})
}

// addAfterInterrupt records the typed text that answered a stopped turn, with
// the thread's human text before the interrupt and its recent tool calls as
// context. A paste-only answer has no typed text and records nothing.
func (s *Scanner) addAfterInterrupt(e transcript.Event, ctx *threadContext, recent *RecentToolCalls) {
	typed, ok := typedText(e.Text)
	if !ok {
		return
	}
	text := whitespaceRun.ReplaceAllString(typed, " ")
	o := Occurrence{Session: e.SessionID, Timestamp: e.Timestamp, Text: text, PreviousHumanText: ctx.previous, RecentToolCalls: recent}
	if ctx.first != ctx.previous {
		o.FirstObservedHumanTextInScan = ctx.first
	}
	addText(s.interrupts, text, o)
}

func addText(m map[string]*textGroup, text string, o Occurrence) {
	g := m[text]
	if g == nil {
		g = &textGroup{sessions: map[string]struct{}{}}
		m[text] = g
	}
	g.sessions[o.Session] = struct{}{}
	g.occurrences = append(g.occurrences, o)
}

// matchLine returns the line containing byte offset pos, for use as a cluster
// snippet.
func matchLine(text string, pos int) string {
	if pos < 0 || pos > len(text) {
		pos = 0
	}
	start := strings.LastIndexByte(text[:pos], '\n') + 1
	end := strings.IndexByte(text[pos:], '\n')
	if end == -1 {
		end = len(text)
	} else {
		end += pos
	}
	return strings.TrimSpace(text[start:end])
}

func normalizeKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	key := strings.TrimSpace(whitespaceRun.ReplaceAllString(b.String(), " "))
	if len(key) > clusterKeyLen {
		key = key[:clusterKeyLen]
	}
	return key
}

// addCluster adds the event to its cluster and returns the occurrence it
// appended, or nil when the key normalizes to nothing.
func (s *Scanner) addCluster(kind, keyBasis, example string, e transcript.Event) *Occurrence {
	key := normalizeKey(keyBasis)
	if key == "" {
		return nil
	}

	m := s.clusters[kind]
	c, ok := m[key]
	if !ok {
		c = &Cluster{Pattern: key, sessionIDs: map[string]struct{}{}}
		m[key] = c
	}

	c.Count++
	if e.SessionID != "" {
		c.sessionIDs[e.SessionID] = struct{}{}
	}
	c.Sessions = len(c.sessionIDs)
	if c.FirstSeen == "" || e.Timestamp < c.FirstSeen {
		c.FirstSeen = e.Timestamp
	}
	if e.Timestamp > c.LastSeen {
		c.LastSeen = e.Timestamp
	}
	text := whitespaceRun.ReplaceAllString(strings.TrimSpace(example), " ")
	c.Occurrences = append(c.Occurrences, Occurrence{
		Session:   e.SessionID,
		Timestamp: e.Timestamp,
		Text:      clip(text, maxOccurrenceLen),
	})
	return &c.Occurrences[len(c.Occurrences)-1]
}

// Finalize assembles the digest from accumulated clusters, ranks each signal's
// clusters and numbers them.
func (s *Scanner) Finalize(windowDays int, projects []string, instructionFiles []InstructionFile) *Digest {
	d := NewDigest(windowDays)
	d.ProjectsScanned = projects
	if instructionFiles != nil {
		d.InstructionFiles = instructionFiles
	}

	s.stats.SessionsScanned = len(s.sessions)
	s.stats.ThreadsScanned = len(s.threads)
	s.stats.Harnesses = s.harnessRows()
	d.Stats = s.stats

	d.Signals.UserCorrections = collect(s.clusters[kindCorrection])
	d.Signals.UserRejections = collect(s.clusters[kindRejection])
	d.Signals.ToolFailures = collect(s.clusters[kindFailure])
	d.Signals.HookBlocks = collect(s.clusters[kindHookBlock])
	d.Signals.RepeatedWording = repeatedWording(s.wording)
	interrupts := map[string]*Cluster{}
	for text, g := range s.interrupts {
		interrupts[text] = textCluster(text, g)
	}
	d.Signals.AfterInterrupt = collect(interrupts)

	for _, list := range d.Signals.lists() {
		d.Stats.ClustersFound += len(list.items)
	}
	d.assignIDs()
	return d
}

// harnessRows renders coverage in registry order, so the digest is
// deterministic whatever order the scan visited harnesses in.
func (s *Scanner) harnessRows() []HarnessCoverage {
	out := []HarnessCoverage{}
	for _, h := range harness.All() {
		if hs, ok := s.coverage[h.ID]; ok {
			out = append(out, hs.cov)
		}
	}
	return out
}

// repeatedWording keeps the texts typed in minRepeatSessions or more sessions,
// numbers each one's send groups, and ranks them by session spread.
func repeatedWording(m map[string]*textGroup) []*Cluster {
	out := []*Cluster{}
	for text, g := range m {
		if len(g.sessions) < minRepeatSessions {
			continue
		}
		c := textCluster(text, g)
		c.SendGroups = assignSendGroups(c.Occurrences)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Sessions != b.Sessions {
			return a.Sessions > b.Sessions
		}
		if a.SendGroups != b.SendGroups {
			return a.SendGroups > b.SendGroups
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Pattern < b.Pattern
	})
	return out
}

// textCluster is one text group as a cluster, its texts clipped.
func textCluster(text string, g *textGroup) *Cluster {
	occ := append([]Occurrence(nil), g.occurrences...)
	c := &Cluster{Pattern: clip(text, maxOccurrenceLen), Count: len(occ), Sessions: len(g.sessions), Occurrences: occ}
	for i := range occ {
		occ[i].Text = clip(occ[i].Text, maxOccurrenceLen)
		if c.FirstSeen == "" || occ[i].Timestamp < c.FirstSeen {
			c.FirstSeen = occ[i].Timestamp
		}
		if occ[i].Timestamp > c.LastSeen {
			c.LastSeen = occ[i].Timestamp
		}
	}
	return c
}

// assignSendGroups orders occurrences by time and numbers their send groups. An
// occurrence joins the open group when it falls within sendGroupWindow of the
// group's earliest member and its session is not in the group yet; otherwise it
// opens the next group. An occurrence whose timestamp does not parse is a group
// of its own. It returns the group count.
func assignSendGroups(occ []Occurrence) int {
	times := make([]time.Time, len(occ))
	parsed := make([]bool, len(occ))
	for i, o := range occ {
		t, err := time.Parse(time.RFC3339Nano, o.Timestamp)
		times[i], parsed[i] = t, err == nil
	}
	idx := make([]int, len(occ))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		i, j := idx[a], idx[b]
		if parsed[i] != parsed[j] {
			return parsed[i]
		}
		if !times[i].Equal(times[j]) {
			return times[i].Before(times[j])
		}
		if occ[i].Timestamp != occ[j].Timestamp {
			return occ[i].Timestamp < occ[j].Timestamp
		}
		return occ[i].Session < occ[j].Session
	})
	sorted := make([]Occurrence, len(occ))
	group := 0
	var start time.Time
	open := false
	members := map[string]struct{}{}
	for k, i := range idx {
		_, dup := members[occ[i].Session]
		if !open || !parsed[i] || dup || times[i].Sub(start) > sendGroupWindow {
			group++
			start, open = times[i], parsed[i]
			members = map[string]struct{}{}
		}
		members[occ[i].Session] = struct{}{}
		sorted[k] = occ[i]
		sorted[k].Group = group
	}
	copy(occ, sorted)
	return group
}

func collect(m map[string]*Cluster) []*Cluster {
	out := make([]*Cluster, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score() != out[j].Score() {
			return out[i].Score() > out[j].Score()
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}
