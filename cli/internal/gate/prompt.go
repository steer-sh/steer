package gate

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
)

// The five prompts, byte-identical to the Python hook's templates. Byte identity is
// load-bearing twice over: the instrument digest is a hash of reviewPrompt, and a
// drifting template silently reclassifies every row that follows.
var (
	//go:embed prompts/review.md
	reviewPrompt string
	//go:embed prompts/deny.md
	denyTemplate string
	//go:embed prompts/cap.md
	capTemplate string
	//go:embed prompts/round-note.md
	roundNote string
	//go:embed prompts/rerun-note.md
	rerunNoteText string
)

// MaxSubjectChars bounds the chain key. The template hands the agent the same
// truncation the row stores, so the two carry one string and match exactly.
const MaxSubjectChars = 100

// NoSubject stands in when the commit message could not be read. It never matches a
// row, which leaves those chains on the bound they were joined by before.
const NoSubject = "this commit"

// CommitSubject is the commit message's first real line, truncated to whole runes.
func CommitSubject(message string) string {
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if runes := []rune(line); len(runes) > MaxSubjectChars {
			line = strings.TrimSpace(string(runes[:MaxSubjectChars]))
		}
		return line
	}
	return ""
}

func fill(tmpl string, fields map[string]string) string {
	pairs := make([]string, 0, len(fields)*2)
	for key, value := range fields {
		pairs = append(pairs, "{"+key+"}", value)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// BuildReviewPrompt assembles the reviewer prompt. `context` is already a rendered
// block (or empty); message and diff must be neutralised by the caller.
func BuildReviewPrompt(message, diff, context string) string {
	return fill(reviewPrompt, map[string]string{
		"message": message, "diff": diff, "context": context,
	})
}

// BuildDeny renders the blocking message the agent reads on a fired gate.
func BuildDeny(count int, review string, firing, cap int, rerun, subject string) string {
	round := ""
	if firing != 1 {
		round = fill(roundNote, map[string]string{
			"firing": fmt.Sprint(firing), "cap": fmt.Sprint(cap),
		})
	}
	if subject == "" {
		subject = NoSubject
	}
	return fill(denyTemplate, map[string]string{
		"count": fmt.Sprint(count), "review": review, "cap": fmt.Sprint(cap),
		"firing": fmt.Sprint(firing), "round_note": round, "rerun_note": rerun,
		"subject": subject,
	})
}

// BuildCap renders the capped chain's terminal, non-blocking review message.
func BuildCap(count int, review string, firing, cap int, subject string) string {
	if subject == "" {
		subject = NoSubject
	}
	return fill(capTemplate, map[string]string{
		"count": fmt.Sprint(count), "review": review,
		"firing": fmt.Sprint(firing), "cap": fmt.Sprint(cap),
		"subject": subject,
	})
}

// RerunNoteText is prepended to the deny only when the denied command writes a file.
// A deny stops the whole command, so a heredoc or `>` inside it never runs and the
// file it would have written is not on disk; a retry of the `git commit` half alone
// then exits 128 on the missing path. Only the Claude Code adapter can tell, so it
// decides and passes the result to BuildDeny.
func RerunNoteText() string { return rerunNoteText }

// PromptDigest is the first 8 hex chars of sha256(reviewPrompt) — the instrument's
// prompt half.
func PromptDigest() string {
	sum := sha256.Sum256([]byte(reviewPrompt))
	return hex.EncodeToString(sum[:])[:8]
}

// InstrumentID identifies the reviewer that produced a row: model, run count, prompt
// digest, implementation, and effort when one is set. Stamped on every row so a later
// reading of the log can tell a change in the code being reviewed from a change in the
// thing doing the reviewing.
//
// No reader splits this string by position, so a fifth segment is safe to append. It
// is appended only when effort is set, which keeps a row from an unconfigured machine
// byte-identical to the rows it already wrote.
func (c Config) InstrumentID() string {
	id := fmt.Sprintf("%s/%d/%s/%s", c.ReviewerModel, c.ReviewerRuns, PromptDigest(), InstrumentSuffix)
	if c.ReviewerEffort != "" {
		id += "/" + c.ReviewerEffort
	}
	return id
}

// InstrumentFields is the same identity by dimension. The string form collapses the
// dimensions, so a reader comparing two of them can answer only "different"; a grader
// that must say WHICH half of the reviewer moved reads this instead.
//
// Provider and model are the reviewer's own answers, so an unconfigured model arrives
// as that reviewer's `<provider>-default` sentinel, never empty and never another
// provider's pin. Effort has no such label: it is recorded as the operator wrote it,
// and empty means the provider's default, because the value that default resolves to
// lives inside the provider CLI.
func (c Config) InstrumentFields() map[string]any {
	return map[string]any{
		"provider":      c.ReviewerProvider,
		"model":         c.ReviewerModel,
		"effort":        c.ReviewerEffort,
		"runs":          c.ReviewerRuns,
		"prompt_digest": PromptDigest(),
		"engine":        InstrumentSuffix,
	}
}
