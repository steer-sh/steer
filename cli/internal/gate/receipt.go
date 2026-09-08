package gate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// CommitIdentity names the commit a receipt is about: the repository, the subject and
// the diff's digest, which is the key the cleared record and the ledger already store.
//
// No commit hash appears here. PreToolUse fires before git runs, so the hash does not
// exist yet.
func CommitIdentity(req Request, raw string, firing int) string {
	var parts []string
	if name := filepath.Base(req.Repo); name != "" && name != "." && name != string(filepath.Separator) {
		parts = append(parts, name)
	}
	if subject := CommitSubject(req.Message); subject != "" {
		parts = append(parts, fmt.Sprintf("%q", subject))
	}
	tail := fmt.Sprintf("firing %d", firing)
	if raw != "" {
		tail += ", diff " + Digest(raw)
	}
	parts = append(parts, "("+tail+")")
	return strings.Join(parts, " ")
}

// proceed builds one receipt for a commit the gate does not block and sends it to both
// channels.
//
// Kind stays the caller's: a path that reviewed and passed still allows, and a path
// that reviewed nothing still declines to rule.
func proceed(kind DecisionKind, req Request, raw string, firing int, sentence string, err error) Decision {
	text := fmt.Sprintf("[commit-gate] %s: %s", CommitIdentity(req, raw, firing), sentence)
	return Decision{Kind: kind, Reason: text, SystemMessage: text, LedgerError: err}
}
