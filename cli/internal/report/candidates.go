package report

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

const (
	// recentToolLimit is how many root-thread tool calls a turn's score reads.
	recentToolLimit = 3

	// correctionFlagScore is the lowest score that flags a turn. The margin
	// absorbs the order a floating-point sum adds its terms in.
	correctionFlagScore = -0.95 - 1e-9

	scoreCutAbove = 1500
	scoreCutHead  = 1000
	scoreCutTail  = 400
	scoreCutMark  = " …[cut]… "
)

// correctionListJSON is the word-score list: {"bias": n, "weights": {feature: n}}.
//
//go:embed correction_list.json
var correctionListJSON []byte

// correctionList parses the embedded list on first use, so a process that scans
// nothing pays for no parse.
var correctionList = sync.OnceValue(func() (list struct {
	Bias    float64            `json:"bias"`
	Weights map[string]float64 `json:"weights"`
}) {
	if err := json.Unmarshal(correctionListJSON, &list); err != nil {
		panic("report: embedded correction list: " + err.Error())
	}
	return list
})

// CorrectionListDigest identifies the embedded list in a report. It stays out of
// ParserInstrument: no session snapshot or fix score reads the list.
func CorrectionListDigest() string {
	sum := sha256.Sum256(correctionListJSON)
	return "list-" + hex.EncodeToString(sum[:])[:12]
}

var scoreToken = regexp.MustCompile(`[a-z0-9']+`)

// scoreLower lowercases as Python's str.lower does for the characters a token can
// hold. U+0130 is the one code point whose lowered form differs in ASCII: Go maps
// it to "i", Python to "i" and a combining dot that ends the token.
func scoreLower(text string) string {
	return strings.ToLower(strings.ReplaceAll(text, "İ", "i̇"))
}

// scoreCut shortens a long text to its head and tail, counted in code points.
func scoreCut(text string) string {
	if utf8.RuneCountInString(text) <= scoreCutAbove {
		return text
	}
	runes := []rune(text)
	return string(runes[:scoreCutHead]) + scoreCutMark + string(runes[len(runes)-scoreCutTail:])
}

// scoreSpace is Python's str.strip set: Unicode whitespace and U+001C to U+001F.
func scoreSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// correctionFeatures returns the sorted distinct features of a typed text and the
// tool names of its recent calls.
func correctionFeatures(text string, tools []string) []string {
	text = scoreCut(text)
	tokens := scoreToken.FindAllString(scoreLower(text), -1)
	set := map[string]struct{}{}
	for n := 1; n <= 5; n++ {
		for j := 0; j+n <= len(tokens); j++ {
			set[strings.Join(tokens[j:j+n], " ")] = struct{}{}
		}
	}
	for n := 1; n <= 4 && n <= len(tokens); n++ {
		set["^"+strings.Join(tokens[:n], " ")] = struct{}{}
	}
	set[lengthBucket(len(tokens))] = struct{}{}
	set[linesBucket(strings.Count(text, "\n")+1)] = struct{}{}
	if strings.HasSuffix(strings.TrimRightFunc(text, scoreSpace), "?") {
		set["end:?"] = struct{}{}
	}
	if len(tools) > 0 {
		set["tools:any"] = struct{}{}
	} else {
		set["tools:0"] = struct{}{}
	}
	for _, tool := range tools {
		name, _, _ := strings.Cut(tool, ":")
		set["tool:"+name] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func lengthBucket(tokens int) string {
	switch {
	case tokens <= 5:
		return "len:1-5"
	case tokens <= 15:
		return "len:6-15"
	case tokens <= 40:
		return "len:16-40"
	case tokens <= 100:
		return "len:41-100"
	case tokens <= 300:
		return "len:101-300"
	}
	return "len:301+"
}

func linesBucket(lines int) string {
	switch {
	case lines == 1:
		return "lines:1"
	case lines <= 5:
		return "lines:2-5"
	case lines <= 20:
		return "lines:6-20"
	}
	return "lines:21+"
}

// correctionScore is the list's score for a typed text. It sums in sorted feature
// order, so one input always yields one value.
func correctionScore(text string, tools []string) float64 {
	list := correctionList()
	sum := 0.0
	for _, f := range correctionFeatures(text, tools) {
		sum += list.Weights[f]
	}
	return list.Bias + sum
}

func correctionFlagged(score float64) bool { return score >= correctionFlagScore }

// addCandidate reads a real root-thread turn for correction_candidates. A typed
// turn closes its tool window whether or not it becomes a candidate; a paste-only
// turn has no typed text and leaves the window open.
func (s *Scanner) addCandidate(e transcript.Event, ctx *threadContext, corrected, answers bool) {
	typed, ok := typedText(e.Text)
	if !ok {
		return
	}
	tools := ctx.recentTools
	ctx.recentTools = nil
	if corrected || steerInvocation.MatchString(typed) {
		return
	}
	score := correctionScore(typed, tools)
	if !correctionFlagged(score) {
		return
	}
	shown := roundScore(score)
	text := whitespaceRun.ReplaceAllString(typed, " ")
	o := Occurrence{Session: e.SessionID, Timestamp: e.Timestamp, Text: whitespaceRun.ReplaceAllString(scoreCut(typed), " "), Score: &shown,
		PreviousHumanText: ctx.previous, AfterInterrupt: answers}
	if ctx.first != ctx.previous {
		o.FirstObservedHumanTextInScan = ctx.first
	}
	addText(s.candidates, text, o)
}

// noteTool keeps the last recentToolLimit tool names of a root thread.
func (ctx *threadContext) noteTool(name string) {
	ctx.recentTools = append(ctx.recentTools, name)
	if len(ctx.recentTools) > recentToolLimit {
		ctx.recentTools = ctx.recentTools[1:]
	}
}

// roundScore cuts a score to three places for display. The comparison assignment
// keeps a sum just below zero from printing as -0.
func roundScore(score float64) float64 {
	r := math.Round(score*1000) / 1000
	if r == 0 {
		r = 0
	}
	return r
}

// correctionCandidates ranks candidate clusters by their highest score, so a reader
// that stops early has read the strongest flags.
func correctionCandidates(m map[string]*textGroup) []*Cluster {
	out := make([]*Cluster, 0, len(m))
	top := map[*Cluster]float64{}
	for text, g := range m {
		c := textCluster(text, g)
		best := math.Inf(-1)
		for i, o := range g.occurrences {
			best = math.Max(best, *o.Score)
			// The reader sees all the text the score read, past the usual clip.
			c.Occurrences[i].Text = o.Text
		}
		top[c] = best
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if top[a] != top[b] {
			return top[a] > top[b]
		}
		if a.Score() != b.Score() {
			return a.Score() > b.Score()
		}
		return a.Pattern < b.Pattern
	})
	return out
}
