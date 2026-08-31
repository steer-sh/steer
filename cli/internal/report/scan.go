package report

import (
	"regexp"
	"sort"
	"strings"
	"time"

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
)

// correctionPatterns match user-turn language indicating the agent repeated
// something the user already corrected. Precision beats recall here: a missed
// correction costs one suggestion, a false positive pollutes the report.
var correctionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(don'?t|do not|stop|quit|never) (use|using|add|adding|write|writing|create|creating|put|putting|make|making|do|doing|hardcode|hardcoding)\b`),
	regexp.MustCompile(`(?i)\bi (said|told you|asked you|already (said|told))\b`),
	regexp.MustCompile(`(?i)\buse \S{1,40} instead\b`),
	regexp.MustCompile(`(?i)^(no|nope|wrong|again)\b[,.!: ]`),
	regexp.MustCompile(`(?i)\bwhy (are|do|did) you (keep|still|always)\b`),
	regexp.MustCompile(`(?i)\b(again|still) (using|doing|adding|writing)\b`),
	regexp.MustCompile(`(?i)\byou (keep|always|still) (us|do|add|writ|creat|mak|forget)`),
}

// failurePattern matches lint/test/compile failures in Bash tool output.
var failurePattern = regexp.MustCompile(`(?i)(error TS\d+|\beslint\b|\bFAILED\b|\bFAIL:|AssertionError|SyntaxError|Traceback \(most recent call last\)|panic:|\bruff\b|\bflake8\b|\bmypy\b|npm ERR!|ERROR in |error\[E\d+\])`)

var whitespaceRun = regexp.MustCompile(`\s+`)

// toolCall is what the scanner remembers about a tool call so the later result
// can be attributed.
type toolCall struct {
	Name   string
	Target string
}

// Scanner accumulates signal clusters from normalized transcript events. It
// reads no harness format of its own: an Adapter supplies the events.
type Scanner struct {
	Since    time.Time
	stats    Stats
	clusters map[string]map[string]*Cluster
	sessions map[string]struct{}
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
	}
}

// ScanSession streams one session through the adapter and classifies its events.
func (s *Scanner) ScanSession(a transcript.Adapter, session transcript.Session) error {
	// tool call id → call info, so results can be attributed. Per-session:
	// results always land in the same transcript as their call.
	calls := map[string]toolCall{}

	counts, err := a.Stream(session, transcript.Filter{Since: s.Since}, func(e transcript.Event) error {
		s.classify(e, calls)
		return nil
	})
	s.stats.FilesScanned++
	s.stats.BytesScanned += counts.Bytes
	s.stats.EntriesScanned += counts.Entries
	s.stats.EntriesSkipped += counts.Skipped
	return err
}

func (s *Scanner) classify(e transcript.Event, calls map[string]toolCall) {
	if e.SessionID != "" {
		s.sessions[e.SessionID] = struct{}{}
	}
	switch e.Kind {
	case transcript.KindToolCall:
		calls[e.ToolUseID] = toolCall{Name: e.Tool, Target: e.Target}
	case transcript.KindHumanTurn:
		if !e.Meta {
			s.checkCorrection(e.Text, e)
		}
	case transcript.KindToolResult:
		s.checkToolResult(e, calls)
	}
}

// checkCorrection matches a human turn against correction language.
func (s *Scanner) checkCorrection(text string, e transcript.Event) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "Caveat:") {
		return
	}
	for _, p := range correctionPatterns {
		loc := p.FindStringIndex(trimmed)
		if loc == nil {
			continue
		}
		snippet := matchLine(trimmed, loc[0])
		s.addCluster(kindCorrection, snippet, snippet, e)
		return
	}
}

// checkToolResult classifies a result as rejection, hook block, or lint/test
// failure.
func (s *Scanner) checkToolResult(e transcript.Event, calls map[string]toolCall) {
	text := e.Text
	if text == "" {
		return
	}
	if len(text) > maxResultScan {
		text = text[:maxResultScan]
	}
	call := calls[e.ToolUseID]
	lower := strings.ToLower(text)

	switch {
	case strings.Contains(lower, "doesn't want to proceed") || strings.Contains(lower, "user rejected"):
		key := call.Name + ": " + call.Target
		s.addCluster(kindRejection, key, key, e)
	case e.IsError && strings.Contains(lower, "hook") && (strings.Contains(lower, "block") || strings.Contains(lower, "denied")):
		snippet := matchLine(text, strings.Index(lower, "hook"))
		s.addCluster(kindHookBlock, snippet, snippet, e)
	case call.Name == "Bash":
		if loc := failurePattern.FindStringIndex(text); loc != nil {
			snippet := matchLine(text, loc[0])
			s.addCluster(kindFailure, snippet, snippet+" — cmd: "+call.Target, e)
		}
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
	if len(c.Examples) < maxExamplesPerCluster {
		ex := whitespaceRun.ReplaceAllString(strings.TrimSpace(example), " ")
		if len(ex) > maxExampleLen {
			ex = ex[:maxExampleLen]
		}
		c.Examples = append(c.Examples, ex)
	}
}

// Finalize assembles the digest from accumulated clusters. Single-occurrence
// correction clusters are kept — the model layer may still find them useful —
// but they rank last and evict first.
func (s *Scanner) Finalize(windowDays int, projects []string, claudeMDs []ClaudeMD) *Digest {
	d := NewDigest(windowDays)
	d.ProjectsScanned = projects
	d.ClaudeMD = claudeMDs

	s.stats.SessionsScanned = len(s.sessions)
	d.Stats = s.stats

	d.Signals.UserCorrections = collect(s.clusters[kindCorrection])
	d.Signals.UserRejections = collect(s.clusters[kindRejection])
	d.Signals.ToolFailures = collect(s.clusters[kindFailure])
	d.Signals.HookBlocks = collect(s.clusters[kindHookBlock])

	return d
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
