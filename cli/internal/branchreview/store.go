// Package branchreview reviews a whole branch before a pull request and records
// what the reviewers found and how the author ruled on it. It is advisory: nothing
// here blocks a command. Its rows live in their own store, apart from the commit
// review's ledger, so no reader of commit-review evidence counts them.
package branchreview

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/jsonl"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Component is the store's directory name under ~/.steer/state. It is new and has
// no legacy corpus, so it is not one of statestore.Components.
const Component = "branch-review"

// Dir is the store's directory.
func Dir() string { return steerpath.StateDir(Component) }

// ReviewsFile is the per-host stream of branch reviews in dir.
func ReviewsFile(dir, host string) string {
	return filepath.Join(dir, "reviews."+host+".jsonl")
}

// RulingsFile is the per-host stream of the author's rulings in dir.
func RulingsFile(dir, host string) string {
	return filepath.Join(dir, "rulings."+host+".jsonl")
}

// The phases a review row records.
const (
	PhaseClean     = "clean"
	PhaseFindings  = "findings"
	PhaseNitpick   = "nitpick"
	PhaseUngated   = "ungated"
	PhaseSkipEmpty = "skip-empty"
	PhaseSkipProse = "skip-prose"
)

// Finding is one parsed finding of a review, numbered across the whole panel.
type Finding struct {
	Index     int    `json:"index"`
	Run       int    `json:"run"`
	Severity  string `json:"severity"`
	File      string `json:"file"`
	Claim     string `json:"claim"`
	FailsWhen string `json:"fails_when"`
	Rule      string `json:"rule,omitempty"`
}

// Review is the part of a review row a ruling reads back.
type Review struct {
	TS       int64     `json:"ts"`
	ReviewID string    `json:"review_id"`
	Phase    string    `json:"phase"`
	Repo     string    `json:"repo"`
	Branch   string    `json:"branch"`
	BaseSHA  string    `json:"base_sha"`
	HeadSHA  string    `json:"head_sha"`
	Commits  int       `json:"commits"`
	Files    int       `json:"files"`
	Slots    int       `json:"finding_slots"`
	Findings []Finding `json:"findings"`
	// Unparsed counts the answered runs whose findings could not be parsed.
	Unparsed int `json:"findings_unparsed"`
}

// LoadReviews reads the host's review rows in dir, oldest first.
func LoadReviews(dir, host string) ([]Review, error) {
	return jsonl.Load(ReviewsFile(dir, host), func(r *Review) bool { return r.ReviewID != "" })
}

// FindReview returns the host's review row with id, or an error naming why not.
func FindReview(dir, host, id string) (Review, error) {
	reviews, err := LoadReviews(dir, host)
	if err != nil {
		return Review{}, err
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		if reviews[i].ReviewID == id {
			return reviews[i], nil
		}
	}
	return Review{}, fmt.Errorf("no branch review %s on this machine (%s)", id, ReviewsFile(dir, host))
}

// Listed is a review with what its latest ruling receipt says.
type Listed struct {
	Review
	// Rulable reports that steer review rule accepts rulings on this review.
	Rulable       bool `json:"rulable"`
	Ruled         bool `json:"ruled"`
	HeadAdvanced  bool `json:"head_advanced"`
	HeadElsewhere bool `json:"head_elsewhere,omitempty"`
}

// List returns the host's reviews of repo, newest first; an empty repo lists every
// repository's.
func List(dir, host, repo string) ([]Listed, error) {
	reviews, err := LoadReviews(dir, host)
	if err != nil {
		return nil, err
	}
	receipts, err := LoadReceipts(dir, host)
	if err != nil {
		return nil, err
	}
	latest := map[string]Receipt{}
	for _, r := range receipts {
		latest[r.ReviewID] = r
	}
	var out []Listed
	for i := len(reviews) - 1; i >= 0; i-- {
		r := reviews[i]
		if repo != "" && r.Repo != repo {
			continue
		}
		receipt, ruled := latest[r.ReviewID]
		out = append(out, Listed{Review: r, Rulable: r.refusal() == nil, Ruled: ruled,
			HeadAdvanced: receipt.HeadAdvanced, HeadElsewhere: receipt.HeadElsewhere})
	}
	return out, nil
}

// appendRow writes one row to path, creating the store private to the user.
func appendRow(path, noun string, row any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return jsonl.Append(path, 0o600, noun, row)
}

// saveDiff writes the diff the reviewers read under dir/diffs and returns the row
// fields naming it, or the error beside no fields.
func saveDiff(dir, text string) (gate.Row, error) {
	sha := gate.Digest(text)
	name := sha + ".patch"
	diffs := filepath.Join(dir, "diffs")
	if err := os.MkdirAll(diffs, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(diffs, name), []byte(text), 0o600); err != nil {
		return nil, err
	}
	return gate.Row{"diff_file": name, "diff_sha": sha, "diff_chars": len(text)}, nil
}

// newReviewID returns twelve random hex characters.
func newReviewID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
