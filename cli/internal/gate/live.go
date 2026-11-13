package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// LiveVersion is the shape version a live review file carries.
const LiveVersion = 1

// The two states of a live review file.
const (
	LiveRunning = "running"
	LiveEnded   = "ended"
)

// liveBeatEvery is how often a running review rewrites its file. A reader takes a
// running file whose beat is several periods old as left by a gate that died.
var liveBeatEvery = 2 * time.Second

// maxLiveClaimChars bounds one finding's summary in a live review file.
const maxLiveClaimChars = 240

// LiveReview is the file that says a round's review runs now, and then how it ended.
// It is a view for a reader that draws the review; no decision reads it.
type LiveReview struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	// ThreadID names the subagent thread the commit came from, empty on the root thread.
	ThreadID string `json:"thread_id,omitempty"`
	Repo     string `json:"repo"`
	ChainID  string `json:"chain_id,omitempty"`
	// Round is the firing; MaxRounds is the block limit in force for it.
	Round     int `json:"round"`
	MaxRounds int `json:"max_rounds"`
	// Final marks the non-blocking review after the block limit.
	Final bool `json:"final,omitempty"`
	// Reviewer and Model are the provider's name and the model label the instrument
	// records.
	Reviewer string `json:"reviewer"`
	Model    string `json:"model,omitempty"`
	Runs     int    `json:"runs"`
	// RunsEnded lists each run that has returned, in the order they returned.
	RunsEnded []LiveRun `json:"runs_ended"`

	State string `json:"state"`
	// StartedMS, BeatMS and EndedMS are Unix milliseconds. BeatMS is the last write
	// of a running review, made every BeatEveryMS.
	StartedMS   int64 `json:"started_ms"`
	BeatMS      int64 `json:"beat_ms"`
	BeatEveryMS int64 `json:"beat_every_ms"`
	// ReviewTimeoutS is each run's wall-clock limit, in seconds.
	ReviewTimeoutS int `json:"review_timeout_s"`
	PID            int `json:"pid"`

	EndedMS int64 `json:"ended_ms,omitempty"`
	// Outcome is the phase of the ledger row the round wrote: deny, clean, nitpick,
	// cap or ungated.
	Outcome string `json:"outcome,omitempty"`
	// Answered counts the runs that returned a usable review.
	Answered int `json:"answered,omitempty"`
	// Findings are the round's well-formed findings, duplicates included: each one's
	// severity, file and claim, the claim cut to maxLiveClaimChars bytes. The FAILS WHEN
	// line stays in the review the agent receives.
	Findings []LiveFinding `json:"findings,omitempty"`
}

// LiveRun is one reviewer run that has returned.
type LiveRun struct {
	// Run is the run's position in the panel, from 1.
	Run       int    `json:"run"`
	Outcome   string `json:"outcome"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// LiveFinding is the summary of one well-formed finding.
type LiveFinding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Claim    string `json:"claim"`
}

// LiveDir holds one directory per session, each with one file per chain key.
func (c Config) LiveDir() string { return filepath.Join(c.StateDir, "live") }

// livePath is the live review file of one request's chain key. A request with no
// session has none.
func (c Config) livePath(req Request) string {
	session := slug(req.SessionID, 64)
	if session == "" {
		return ""
	}
	return filepath.Join(c.LiveDir(), session,
		slugOrDefault(req.Actor, 64)+"-"+repoHash(Toplevel(req.Repo))+".json")
}

// liveReview keeps one running review's file current. Every method accepts a nil
// receiver and reports no failure: the file is never allowed to change a review.
type liveReview struct {
	mu   sync.Mutex
	path string
	doc  LiveReview
	stop chan struct{}
}

// beginLive writes the running file for a round and starts its beat. It returns nil
// for a request with no session.
func (e *Engine) beginLive(req Request, firing int, final bool) *liveReview {
	path := e.cfg.livePath(req)
	if path == "" {
		return nil
	}
	pruneLive(e.cfg)
	now := time.Now().UnixMilli()
	live := &liveReview{path: path, stop: make(chan struct{}), doc: LiveReview{
		Version: LiveVersion, SessionID: req.SessionID, ThreadID: req.ThreadID,
		Repo: req.Repo, ChainID: req.chainID,
		Round: firing, MaxRounds: e.cfg.MaxFirings, Final: final,
		Reviewer: e.cfg.ReviewerProvider, Model: e.cfg.ReviewerModel,
		Runs: e.cfg.ReviewerRuns, RunsEnded: []LiveRun{},
		State: LiveRunning, StartedMS: now, BeatMS: now,
		BeatEveryMS:    liveBeatEvery.Milliseconds(),
		ReviewTimeoutS: int(e.cfg.ReviewTimeout.Seconds()), PID: os.Getpid(),
	}}
	live.write()
	go live.beat()
	return live
}

// beat rewrites the running file every period until the review ends.
func (l *liveReview) beat() {
	ticker := time.NewTicker(liveBeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.mu.Lock()
			if l.doc.State == LiveRunning {
				l.doc.BeatMS = time.Now().UnixMilli()
				l.write()
			}
			l.mu.Unlock()
		}
	}
}

// runEnded records one run's return. The panel calls it from each run's goroutine.
func (l *liveReview) runEnded(index int, run ReviewRun) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.doc.State != LiveRunning {
		return
	}
	l.doc.RunsEnded = append(l.doc.RunsEnded, LiveRun{
		Run: index + 1, Outcome: string(run.Outcome), ElapsedMS: run.Elapsed.Milliseconds()})
	l.doc.BeatMS = time.Now().UnixMilli()
	l.write()
}

// end records how the round ended, under the phase of the row it wrote.
func (l *liveReview) end(phase string, result PanelResult) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.doc.State != LiveRunning {
		return
	}
	close(l.stop)
	l.doc.State = LiveEnded
	l.doc.EndedMS = time.Now().UnixMilli()
	l.doc.Outcome = phase
	l.doc.Answered = result.Answered
	l.doc.Findings = liveFindings(result.Reviews)
	l.write()
}

// write replaces the file through a temporary name beside it, so a reader never
// sees a partly written file. The caller holds mu, or is the only holder of l.
func (l *liveReview) write() {
	raw, err := json.Marshal(l.doc)
	if err != nil {
		return
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".live-*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(raw, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(name, l.path) != nil {
		os.Remove(name)
	}
}

// clearLive removes the live review file of a request's chain key: the retry that
// answers a held round ends what the file said. A retry that puts the held round back
// unanswered keeps it.
func (e *Engine) clearLive(req Request) {
	if path := e.cfg.livePath(req); path != "" {
		os.Remove(path)
	}
}

// liveFindings summarises the well-formed findings of a round's reviews in review order.
func liveFindings(reviews []string) []LiveFinding {
	var out []LiveFinding
	for _, review := range reviews {
		for _, m := range reviewFindingRe.FindAllStringSubmatch(review, -1) {
			out = append(out, LiveFinding{Severity: strings.ToUpper(m[1]),
				File: m[2], Claim: truncateRunes(m[3], maxLiveClaimChars)})
		}
	}
	return out
}

// truncateRunes cuts text to at most n bytes without splitting a UTF-8 character.
func truncateRunes(text string, n int) string {
	cut := truncate(text, n)
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// pruneLive drops live review files no writer has touched for StaleAfter, and the
// session directories that leaves empty.
func pruneLive(cfg Config) {
	sessions, err := os.ReadDir(cfg.LiveDir())
	if err != nil {
		return
	}
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		dir := filepath.Join(cfg.LiveDir(), session.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, file := range files {
			info, err := file.Info()
			if err != nil || time.Since(info.ModTime()) < cfg.StaleAfter {
				continue
			}
			os.Remove(filepath.Join(dir, file.Name()))
		}
		// Fails while the directory still holds a file, which keeps it.
		os.Remove(dir)
	}
}
