package report

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/shellsyntax"
	"github.com/steer-sh/steer-cli/internal/transcript"
)

const (
	kindCorrection = "user_corrections"
	kindRejection  = "user_rejections"
	kindFailure    = "tool_failures"
	kindHookBlock  = "hook_blocks"

	// tool_result payloads can be huge (full test runs); only the head is
	// needed to classify them.
	maxResultScan = 4096

	// rejectionEnvelope opens the error result Claude Code writes for a tool call
	// the user declined at the permission prompt, lowercased.
	rejectionEnvelope = "the user doesn't want to proceed with this tool use"
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
	trimmed := strings.TrimSpace(pastedBlock.ReplaceAllString(text, ""))
	if trimmed == "" || strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "Caveat:") {
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
	case e.IsError && strings.HasPrefix(strings.TrimSpace(lower), rejectionEnvelope):
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
	Since    time.Time
	stats    Stats
	clusters map[string]map[string]*Cluster
	sessions map[string]struct{}
	threads  map[string]struct{}
	coverage map[harness.ID]*harnessScan
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
		sessions: map[string]struct{}{},
		threads:  map[string]struct{}{},
		coverage: map[harness.ID]*harnessScan{},
	}
}

// ScanSession streams one session through the adapter and classifies its events.
func (s *Scanner) ScanSession(a transcript.Adapter, session transcript.Session) error {
	// One classifier per session: a tool result is attributed through the call it
	// answers, and results always land in the same transcript as their call.
	c := NewClassifier()
	hs := s.harnessScanFor(session.Harness)

	counts, err := a.Stream(session, transcript.Filter{Since: s.Since}, func(e transcript.Event) error {
		s.classify(e, c, hs)
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

func (s *Scanner) classify(e transcript.Event, c *Classifier, hs *harnessScan) {
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
	if sig, ok := c.Classify(e); ok {
		s.addCluster(sig.Kind, sig.Key, sig.Example, e)
	}
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

func (s *Scanner) addCluster(kind, keyBasis, example string, e transcript.Event) {
	key := normalizeKey(keyBasis)
	if key == "" {
		return
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
