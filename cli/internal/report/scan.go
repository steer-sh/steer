package report

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
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

// correctionPatterns match user-turn language indicating Claude repeated
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

// transcript entry shapes — only the fields the scanner reads.
type entry struct {
	Type        string   `json:"type"`
	IsMeta      bool     `json:"isMeta"`
	IsSidechain bool     `json:"isSidechain"`
	Timestamp   string   `json:"timestamp"`
	SessionID   string   `json:"sessionId"`
	Message     *message `json:"message"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolUseID string          `json:"tool_use_id"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type toolUseInput struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
}

// toolCall is what the scanner remembers about an assistant tool_use so the
// later tool_result can be attributed.
type toolCall struct {
	Name   string
	Target string
}

// Scanner streams transcript files and accumulates signal clusters.
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

// ScanFile streams one transcript. Unparsable lines are skipped, never fatal:
// a partially-written transcript must not sink the whole report.
func (s *Scanner) ScanFile(tf TranscriptFile) error {
	f, err := os.Open(tf.Path)
	if err != nil {
		return err
	}
	defer f.Close()

	s.stats.FilesScanned++

	// tool_use id → call info, so tool_results can be attributed. Per-file:
	// results always land in the same transcript as their call.
	calls := map[string]toolCall{}

	reader := bufio.NewReaderSize(f, 256*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.stats.BytesScanned += int64(len(line))
			s.scanLine(line, calls)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *Scanner) scanLine(line []byte, calls map[string]toolCall) {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		s.stats.EntriesSkipped++
		return
	}

	if e.Type != "user" && e.Type != "assistant" {
		s.stats.EntriesSkipped++
		return
	}
	if e.IsSidechain || e.Message == nil {
		s.stats.EntriesSkipped++
		return
	}
	if !s.inWindow(e.Timestamp) {
		s.stats.EntriesSkipped++
		return
	}

	s.stats.EntriesScanned++
	if e.SessionID != "" {
		s.sessions[e.SessionID] = struct{}{}
	}

	if e.Type == "assistant" {
		s.recordToolCalls(e.Message.Content, calls)
		return
	}

	// User entry: plain text (a human turn) or tool_result blocks.
	var text string
	if err := json.Unmarshal(e.Message.Content, &text); err == nil {
		if !e.IsMeta {
			s.checkCorrection(text, e)
		}
		return
	}

	var blocks []contentBlock
	if err := json.Unmarshal(e.Message.Content, &blocks); err != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if !e.IsMeta {
				s.checkCorrection(b.Text, e)
			}
		case "tool_result":
			s.checkToolResult(b, calls, e)
		}
	}
}

func (s *Scanner) recordToolCalls(raw json.RawMessage, calls map[string]toolCall) {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" || b.ID == "" {
			continue
		}
		var input toolUseInput
		_ = json.Unmarshal(b.Input, &input)
		target := input.Command
		if target == "" {
			target = input.FilePath
		}
		calls[b.ID] = toolCall{Name: b.Name, Target: target}
	}
}

// checkCorrection matches a human turn against correction language.
func (s *Scanner) checkCorrection(text string, e entry) {
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

// checkToolResult classifies a tool_result as rejection, hook block, or
// lint/test failure.
func (s *Scanner) checkToolResult(b contentBlock, calls map[string]toolCall, e entry) {
	text := resultText(b)
	if text == "" {
		return
	}
	if len(text) > maxResultScan {
		text = text[:maxResultScan]
	}
	call := calls[b.ToolUseID]
	lower := strings.ToLower(text)

	switch {
	case strings.Contains(lower, "doesn't want to proceed") || strings.Contains(lower, "user rejected"):
		key := call.Name + ": " + call.Target
		s.addCluster(kindRejection, key, key, e)
	case b.IsError && strings.Contains(lower, "hook") && (strings.Contains(lower, "block") || strings.Contains(lower, "denied")):
		snippet := matchLine(text, strings.Index(lower, "hook"))
		s.addCluster(kindHookBlock, snippet, snippet, e)
	case call.Name == "Bash":
		if loc := failurePattern.FindStringIndex(text); loc != nil {
			snippet := matchLine(text, loc[0])
			s.addCluster(kindFailure, snippet, snippet+" — cmd: "+call.Target, e)
		}
	}
}

// resultText flattens a tool_result content payload (string or text blocks).
func resultText(b contentBlock) string {
	var text string
	if err := json.Unmarshal(b.Content, &text); err == nil {
		return text
	}
	var blocks []contentBlock
	if err := json.Unmarshal(b.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, inner := range blocks {
		if inner.Type == "text" && inner.Text != "" {
			parts = append(parts, inner.Text)
		}
	}
	return strings.Join(parts, "\n")
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

func (s *Scanner) addCluster(kind, keyBasis, example string, e entry) {
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

func (s *Scanner) inWindow(timestamp string) bool {
	if timestamp == "" || s.Since.IsZero() {
		return true
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return true
	}
	return !t.Before(s.Since)
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
