package cmd

import (
	"strings"
	"testing"
)

func TestHeadStateNamesWhatTheGateReads(t *testing.T) {
	atHead := map[string]string{"live": "style is not a finding", "moved": "the old wording"}
	for id, want := range map[string]string{
		"live":    "live at HEAD",
		"moved":   "differs from HEAD",
		"missing": "not at HEAD",
	} {
		text := "style  is not a finding"
		if id == "moved" {
			text = "the new wording"
		}
		if got := headState(atHead, id, text); !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}
