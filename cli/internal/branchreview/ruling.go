package branchreview

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/jsonl"
)

// The verdicts a ruling takes, and the one qualifier an acceptance may carry.
const (
	VerdictAccepted = "ACCEPTED"
	VerdictRejected = "REJECTED"
	QualifierNotFix = "not fixed"
)

// RulingInput is one ruling on one defect as the author writes it. It names the
// findings that report the defect with Index, for one, or Indices, for several
// reviewers' reports of the same defect.
type RulingInput struct {
	Index      int    `json:"index,omitempty"`
	Indices    []int  `json:"indices,omitempty"`
	Verdict    string `json:"verdict"`
	Qualifier  string `json:"qualifier"`
	Resolution string `json:"resolution"`
}

// Ruling is a ruling on one defect with the finding it answers copied beside it. A
// recorded ruling names its findings by Indices alone, ascending; a receipt from
// before Indices names one by Index, and LoadReceipts moves it into Indices. For a
// defect several findings report, File and Claim are the lowest index's, Severity
// the most severe, and Rule the first a finding names.
type Ruling struct {
	RulingInput
	Severity string `json:"severity"`
	File     string `json:"file"`
	Claim    string `json:"claim"`
	Rule     string `json:"rule,omitempty"`
}

// Receipt is one recorded set of rulings on a review.
type Receipt struct {
	TS            int64  `json:"ts"`
	ReviewID      string `json:"review_id"`
	Repo          string `json:"repo"`
	Host          string `json:"host"`
	ReviewHeadSHA string `json:"review_head_sha"`
	RuledHeadSHA  string `json:"ruled_head_sha"`
	RuledTreeSHA  string `json:"ruled_tree_sha"`
	// HeadAdvanced reports that commits landed on the reviewed head between the
	// review and the ruling; HeadElsewhere that HEAD moved to a commit that does not
	// descend from it.
	HeadAdvanced  bool   `json:"head_advanced"`
	HeadElsewhere bool   `json:"head_elsewhere,omitempty"`
	Harness       string `json:"harness"`
	// Accepted, AcceptedNotFixed and Rejected count rulings, one per defect.
	Accepted         int      `json:"accepted"`
	AcceptedNotFixed int      `json:"accepted_not_fixed"`
	Rejected         int      `json:"rejected"`
	Rulings          []Ruling `json:"rulings"`
}

// LoadReceipts reads the host's ruling receipts in dir, oldest first.
func LoadReceipts(dir, host string) ([]Receipt, error) {
	return jsonl.Load(RulingsFile(dir, host), func(r *Receipt) bool {
		for i := range r.Rulings {
			if in := &r.Rulings[i].RulingInput; len(in.Indices) == 0 && in.Index > 0 {
				in.Indices, in.Index = []int{in.Index}, 0
			}
		}
		return r.ReviewID != ""
	})
}

// severityRank orders the reviewer's severity levels, most severe highest.
var severityRank = map[string]int{"NITPICK": 1, "SUBSTANTIVE": 2, "BLOCKING": 3}

// Rule checks rulings against review, one per defect with each finding in exactly
// one, and records the receipt stamped with repo's current HEAD. A review whose findings were not all parsed
// cannot be ruled finding by finding, so it is refused.
func Rule(dir, host, repo, harnessID string, review Review, rulings []RulingInput) (Receipt, error) {
	if repo != review.Repo {
		return Receipt{}, fmt.Errorf("review %s was of %s, not %s; run this from that repository",
			review.ReviewID, review.Repo, repo)
	}
	if err := review.refusal(); err != nil {
		return Receipt{}, err
	}
	byIndex := map[int]Finding{}
	for _, f := range review.Findings {
		byIndex[f.Index] = f
	}
	seen := map[int]bool{}
	receipt := Receipt{ReviewID: review.ReviewID, Repo: repo, Host: host,
		ReviewHeadSHA: review.HeadSHA, Harness: harnessID}
	for _, in := range rulings {
		indices, err := rulingIndices(in)
		if err != nil {
			return Receipt{}, err
		}
		var group []Finding
		for _, i := range indices {
			f, ok := byIndex[i]
			if !ok {
				return Receipt{}, fmt.Errorf("review %s has no finding %d", review.ReviewID, i)
			}
			if seen[i] {
				return Receipt{}, fmt.Errorf("finding %d is ruled twice", i)
			}
			seen[i] = true
			group = append(group, f)
		}
		if err := checkRuling(in); err != nil {
			return Receipt{}, fmt.Errorf("finding %d: %w", indices[0], err)
		}
		in.Index, in.Indices = 0, indices
		switch {
		case in.Verdict == VerdictRejected:
			receipt.Rejected++
		case in.Qualifier == QualifierNotFix:
			receipt.AcceptedNotFixed++
		default:
			receipt.Accepted++
		}
		receipt.Rulings = append(receipt.Rulings, rulingOf(in, group))
	}
	if missing := len(byIndex) - len(seen); missing > 0 {
		var unruled []string
		for _, f := range review.Findings {
			if !seen[f.Index] {
				unruled = append(unruled, fmt.Sprint(f.Index))
			}
		}
		return Receipt{}, fmt.Errorf("every finding needs to be in one ruling; %d are unruled: %s", missing, strings.Join(unruled, ", "))
	}
	head, ok := gate.Git(repo, gitWait, "rev-parse", "HEAD")
	if !ok {
		return Receipt{}, errors.New("git could not read HEAD")
	}
	tree, _ := gate.Git(repo, gitWait, "rev-parse", "HEAD^{tree}")
	receipt.TS = time.Now().Unix()
	receipt.RuledHeadSHA = strings.TrimSpace(head)
	receipt.RuledTreeSHA = strings.TrimSpace(tree)
	if receipt.RuledHeadSHA != review.HeadSHA {
		_, descends := gate.Git(repo, gitWait, "merge-base", "--is-ancestor", review.HeadSHA, receipt.RuledHeadSHA)
		receipt.HeadAdvanced = descends
		receipt.HeadElsewhere = !descends
	}
	return receipt, appendRow(RulingsFile(dir, host), "ruling receipt", receipt)
}

// rulingIndices is the ascending set of findings in names, from Index or Indices but
// not both.
func rulingIndices(in RulingInput) ([]int, error) {
	switch {
	case in.Index != 0 && len(in.Indices) > 0:
		return nil, fmt.Errorf("a ruling names its findings by index or by indices, not both (index %d, indices %v)", in.Index, in.Indices)
	case len(in.Indices) > 0:
		indices := slices.Clone(in.Indices)
		slices.Sort(indices)
		for i := 1; i < len(indices); i++ {
			if indices[i] == indices[i-1] {
				return nil, fmt.Errorf("finding %d is ruled twice", indices[i])
			}
		}
		return indices, nil
	case in.Index != 0:
		return []int{in.Index}, nil
	}
	return nil, errors.New("a ruling names no finding; give it an index or indices")
}

// rulingOf is in with its findings, ascending by index, copied beside it.
func rulingOf(in RulingInput, group []Finding) Ruling {
	r := Ruling{RulingInput: in, Severity: group[0].Severity, File: group[0].File, Claim: group[0].Claim}
	for _, f := range group {
		if severityRank[f.Severity] > severityRank[r.Severity] {
			r.Severity = f.Severity
		}
		if r.Rule == "" {
			r.Rule = f.Rule
		}
	}
	return r
}

func checkRuling(in RulingInput) error {
	switch in.Verdict {
	case VerdictAccepted:
		if in.Qualifier != "" && in.Qualifier != QualifierNotFix {
			return fmt.Errorf("qualifier %q: an acceptance takes %q or none", in.Qualifier, QualifierNotFix)
		}
	case VerdictRejected:
		if in.Qualifier != "" {
			return fmt.Errorf("a rejection takes no qualifier, got %q", in.Qualifier)
		}
	default:
		return fmt.Errorf("verdict %q: expected %s or %s", in.Verdict, VerdictAccepted, VerdictRejected)
	}
	if strings.TrimSpace(in.Resolution) == "" {
		return errors.New("the resolution is empty; say in one sentence what you did or why the premise is false")
	}
	return nil
}

// refusal says why review takes no rulings, or is nil when it does.
func (r Review) refusal() error {
	switch r.Phase {
	case PhaseClean, PhaseFindings, PhaseNitpick:
	default:
		return fmt.Errorf("review %s ended %s: no reviewer's answer is there to rule on",
			r.ReviewID, r.Phase)
	}
	if r.Unparsed > 0 {
		return fmt.Errorf("review %s holds %d reply(ies) whose findings could not be parsed, so "+
			"they cannot be ruled one by one", r.ReviewID, r.Unparsed)
	}
	return nil
}
