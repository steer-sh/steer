package gate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// CommitIdentity names the commit a receipt is about: the repository, the subject and
// the diff's digest, which is the key the cleared record and the ledger already store.
// A placeholder subject is an instruction to the reviewer and is left out.
//
// No commit hash appears here. PreToolUse fires before git runs, so the hash does not
// exist yet.
func CommitIdentity(req Request, raw string, firing int) string {
	parts := commitNameParts(req)
	tail := fmt.Sprintf("firing %d", firing)
	if raw != "" {
		tail += ", diff " + Digest(raw)
	}
	parts = append(parts, "("+tail+")")
	return strings.Join(parts, " ")
}

// CommitName names the commit by its repository and subject alone, for a line about a
// commit the gate did not review.
func CommitName(req Request) string {
	parts := commitNameParts(req)
	if len(parts) == 0 {
		return "commit"
	}
	return strings.Join(parts, " ")
}

func commitNameParts(req Request) []string {
	var parts []string
	if name := filepath.Base(req.Repo); name != "" && name != "." && name != string(filepath.Separator) {
		parts = append(parts, name)
	}
	if subject := CommitSubject(req.Message); subject != "" && !PlaceholderSubject(subject) {
		parts = append(parts, fmt.Sprintf("%q", subject))
	}
	return parts
}

// proceed builds the receipt for a commit the gate does not block. Both channels carry
// the same sentence; only the agent's copy names the firing and the diff digest.
//
// Kind stays the caller's: a path that reviewed and passed still allows, and a path
// that reviewed nothing still declines to rule.
func proceed(kind DecisionKind, req Request, raw string, firing int, sentence string, err error) Decision {
	return Decision{Kind: kind, Reason: agentReceipt(req, raw, firing, sentence),
		SystemMessage: fmt.Sprintf("[commit-gate] %s: %s", CommitName(req), sentence),
		Receipt:       true, LedgerError: err}
}

// agentReceipt is the agent's copy of a let-through receipt: the sentence under the
// commit's full identity.
func agentReceipt(req Request, raw string, firing int, sentence string) string {
	return fmt.Sprintf("[commit-gate] %s: %s", CommitIdentity(req, raw, firing), sentence)
}
