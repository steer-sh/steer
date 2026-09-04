package report

import (
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
)

// Adapters dispatches by a switch, so a harness declaring transcripts and
// carrying no case reads as a harness with no evidence at all — and nothing
// else in the loop would report the gap.
func TestEveryTranscriptHarnessHasAnAdapter(t *testing.T) {
	wanted := 0
	for _, h := range harness.All() {
		if !h.Capabilities.Transcripts {
			continue
		}
		wanted++
		adapters := Adapters(h.ID)
		if len(adapters) != 1 {
			t.Fatalf("%s declares transcripts and Adapters returned %d", h.ID, len(adapters))
		}
		if got := adapters[0].Harness(); got != h.ID {
			t.Errorf("Adapters(%s) returned an adapter for %s", h.ID, got)
		}
	}
	if got := len(Adapters(harness.Unknown)); got != wanted {
		t.Errorf("Adapters(Unknown) returned %d adapters; %d harnesses declare transcripts",
			got, wanted)
	}
}
