package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
)

// The list trims a digest, so it must read one that is always long enough. A
// hand-edited manifest can carry a short stamp; recomputing over the stored text
// cannot.
func TestTheListedDigestIsAlwaysLongEnoughToTrim(t *testing.T) {
	for _, text := range []string{"", "a", "a much longer rule with several words"} {
		digest := claude.HashRule(text)
		if len(digest) < 16 {
			t.Fatalf("HashRule(%q) = %q, too short to trim", text, digest)
		}
		if strings.TrimSpace(digest) != digest {
			t.Fatalf("HashRule(%q) = %q", text, digest)
		}
	}
}
