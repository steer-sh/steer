package codex

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
)

const currentUsage = `{"timestamp":"2026-09-04T04:10:40.138Z","ordinal":16,"type":"token_usage_record","payload":{"usage":{"input_tokens":26969,"cached_input_tokens":10880,"cache_write_input_tokens":0,"output_tokens":206,"total_tokens":27175}}}
`

const legacyUsage = `{"timestamp":"2026-03-21T03:57:59.344Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":633835,"cached_input_tokens":566656,"total_tokens":648859},"last_token_usage":{"input_tokens":65962,"cached_input_tokens":65280,"total_tokens":69607},"model_context_window":258400}}}
`

func TestContextTokensReadsTheCurrentRecord(t *testing.T) {
	// input_tokens already counts cached_input_tokens, so the depth is that one
	// field: summing the cache terms would count 10880 of them twice.
	got, ok := contextTokens(strings.Split(rollout+currentUsage, "\n"))
	if !ok || got != 26969 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestContextTokensReadsTheLastTurnOfTheLegacyRecord(t *testing.T) {
	got, ok := contextTokens(strings.Split(rollout+legacyUsage, "\n"))
	if !ok || got != 65962 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestContextTokensTakesTheNewestRecord(t *testing.T) {
	older := strings.Replace(currentUsage, "26969", "100", 1)
	got, ok := contextTokens(strings.Split(older+currentUsage, "\n"))
	if !ok || got != 26969 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestContextTokensPrefersTheCurrentRecordOverTheLegacyOne(t *testing.T) {
	got, ok := contextTokens(strings.Split(currentUsage+legacyUsage, "\n"))
	if !ok || got != 26969 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestContextTokensOfARolloutWithNoUsage(t *testing.T) {
	if got, ok := contextTokens(strings.Split(rollout, "\n")); ok || got != 0 {
		t.Fatalf("depth = %d, ok = %v", got, ok)
	}
}

func TestAuthorFieldsStampsTheContextDepth(t *testing.T) {
	cfg := testConfig(t)
	path := writeRollout(t, rollout+currentUsage)
	fields := AuthorFields(gate.NewLedger(cfg), cfg, path, "session-1", "gpt-5.6-sol")
	if fields["context_tokens"] != 26969 {
		t.Fatalf("context_tokens = %v", fields["context_tokens"])
	}
}

func TestAuthorFieldsStampsNoDepthWithoutUsage(t *testing.T) {
	cfg := testConfig(t)
	fields := AuthorFields(gate.NewLedger(cfg), cfg, writeRollout(t, rollout), "session-1", "m")
	if _, ok := fields["context_tokens"]; ok {
		t.Fatalf("depth stamped with no usage row: %#v", fields)
	}
}
