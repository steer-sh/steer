package reviewers

import (
	"encoding/json"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// Each provider's usage decodes into gate.TokenUsage, whose Input includes the
// cached part and whose Output includes the reasoning part. A field the reply does
// not carry stays nil.

// claudeUsage is the usage object of a `claude -p --output-format json` reply.
// Claude's input_tokens excludes both cache terms: a reply reads
// input_tokens 2, cache_creation_input_tokens 3288, cache_read_input_tokens 3223
// for a prompt of about 6.5k tokens, so an input missing either cache term is left
// out. Its output_tokens includes thinking.
type claudeUsage struct {
	InputTokens         *int `json:"input_tokens"`
	CacheCreationTokens *int `json:"cache_creation_input_tokens"`
	CacheReadTokens     *int `json:"cache_read_input_tokens"`
	OutputTokens        *int `json:"output_tokens"`
	OutputDetails       *struct {
		ThinkingTokens *int `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

// claudeTokenUsage normalizes a Claude reply's usage and its total_cost_usd.
func claudeTokenUsage(u *claudeUsage, costUSD *float64) *gate.TokenUsage {
	out := &gate.TokenUsage{CostUSD: costUSD}
	if u != nil {
		if u.InputTokens != nil && u.CacheCreationTokens != nil && u.CacheReadTokens != nil {
			input := *u.InputTokens + *u.CacheCreationTokens + *u.CacheReadTokens
			out.Input = &input
		}
		out.CachedInput = u.CacheReadTokens
		out.Output = u.OutputTokens
		if u.OutputDetails != nil {
			out.Reasoning = u.OutputDetails.ThinkingTokens
		}
	}
	return out.Reported()
}

// agyUsage is the usage object of an `agy --output-format json` reply. Whether its
// input_tokens counts cache_read_tokens, and its output_tokens thinking_tokens, is
// read from total_tokens, which a reply gives as the sum of input and output.
type agyUsage struct {
	InputTokens     *int `json:"input_tokens"`
	OutputTokens    *int `json:"output_tokens"`
	ThinkingTokens  *int `json:"thinking_tokens"`
	CacheReadTokens *int `json:"cache_read_tokens"`
	TotalTokens     *int `json:"total_tokens"`
}

// tokenUsage normalizes u. An input or output total that total_tokens cannot settle
// is left out.
func (u *agyUsage) tokenUsage() *gate.TokenUsage {
	if u == nil {
		return nil
	}
	out := &gate.TokenUsage{CachedInput: u.CacheReadTokens, Reasoning: u.ThinkingTokens}
	inputs := readings(u.InputTokens, u.CacheReadTokens)
	outputs := readings(u.OutputTokens, u.ThinkingTokens)
	if len(inputs) == 1 {
		out.Input = &inputs[0]
	}
	if len(outputs) == 1 {
		out.Output = &outputs[0]
	}
	if (len(inputs) > 1 || len(outputs) > 1) && len(inputs) > 0 && len(outputs) > 0 && u.TotalTokens != nil {
		var matches [][2]int
		for _, in := range inputs {
			for _, o := range outputs {
				if in+o == *u.TotalTokens {
					matches = append(matches, [2]int{in, o})
				}
			}
		}
		if len(matches) == 1 {
			out.Input, out.Output = &matches[0][0], &matches[0][1]
		}
	}
	return out.Reported()
}

// readings lists the totals base may stand for: base alone, and base plus part when
// part is non-zero and base may exclude it. A nil base, or a nil part that base may
// exclude, has none.
func readings(base, part *int) []int {
	if base == nil || part == nil {
		return nil
	}
	if *part == 0 {
		return []int{*base}
	}
	return []int{*base, *base + *part}
}

// codexUsage is the usage of a `codex exec --json` turn.completed event. Codex's
// input_tokens already includes cached_input_tokens, and its output_tokens
// reasoning_output_tokens.
type codexUsage struct {
	InputTokens           *int `json:"input_tokens"`
	CachedInputTokens     *int `json:"cached_input_tokens"`
	OutputTokens          *int `json:"output_tokens"`
	ReasoningOutputTokens *int `json:"reasoning_output_tokens"`
}

func (u *codexUsage) tokenUsage() *gate.TokenUsage {
	if u == nil {
		return nil
	}
	return (&gate.TokenUsage{Input: u.InputTokens, CachedInput: u.CachedInputTokens,
		Output: u.OutputTokens, Reasoning: u.ReasoningOutputTokens}).Reported()
}

// chatUsage is the usage object of a chat completions response. prompt_tokens
// includes prompt_tokens_details.cached_tokens, and completion_tokens
// completion_tokens_details.reasoning_tokens.
type chatUsage struct {
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// apiUsage sums chat completions usage over one run's replies. A field enters the
// run's usage only when every reply reported it, a reply with no usage object
// included, so a total never counts some requests and silently skips others.
type apiUsage struct {
	replies                                               int
	prompt, completion, cached, reasoning                 int
	withPrompt, withCompletion, withCached, withReasoning int
}

// add counts one parsed reply, u being its usage object or nil.
func (a *apiUsage) add(u *chatUsage) {
	a.replies++
	if u == nil {
		return
	}
	count := func(v *int, sum, with *int) {
		if v != nil {
			*sum += *v
			*with++
		}
	}
	count(u.PromptTokens, &a.prompt, &a.withPrompt)
	count(u.CompletionTokens, &a.completion, &a.withCompletion)
	if d := u.PromptTokensDetails; d != nil {
		count(d.CachedTokens, &a.cached, &a.withCached)
	}
	if d := u.CompletionTokensDetails; d != nil {
		count(d.ReasoningTokens, &a.reasoning, &a.withReasoning)
	}
}

// tokenUsage is the run's normalized usage, nil when no field was reported by
// every reply.
func (a *apiUsage) tokenUsage() *gate.TokenUsage {
	if a.replies == 0 {
		return nil
	}
	every := func(sum, with int) *int {
		if with != a.replies {
			return nil
		}
		return &sum
	}
	return (&gate.TokenUsage{
		Input:       every(a.prompt, a.withPrompt),
		CachedInput: every(a.cached, a.withCached),
		Output:      every(a.completion, a.withCompletion),
		Reasoning:   every(a.reasoning, a.withReasoning),
	}).Reported()
}

// decodeClaudeUsage reads a Claude reply's usage, nil when stdout is no reply.
func decodeClaudeUsage(data []byte) *gate.TokenUsage {
	var reply claudeReply
	if json.Unmarshal(data, &reply) != nil {
		return nil
	}
	return claudeTokenUsage(reply.Usage, reply.TotalCostUSD)
}

// decodeAgyUsage reads an agy reply's usage, nil when stdout is no reply.
func decodeAgyUsage(data []byte) *gate.TokenUsage {
	var reply agyReply
	if json.Unmarshal(data, &reply) != nil {
		return nil
	}
	return reply.Usage.tokenUsage()
}
