// Package compact summarizes older conversation turns to reclaim context. It
// talks to the model only through apiclient.Client, so it is independent of the
// inference backend — the Korai SDK swap requires no change here.
package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/condense"
)

// DefaultKeepRecent is how many trailing messages are preserved verbatim.
const DefaultKeepRecent = 4

// DefaultThreshold is the estimated-token size above which auto-compaction
// triggers. Chosen well under typical context windows to leave room for the
// next turn's output. It is the ceiling even when a larger context window is
// discovered; see Threshold.
const DefaultThreshold = 120_000

// imageTokenEstimate is the flat per-image token cost used by EstimateTokens.
// Vision backends bill an image by its patch grid, not its byte size; 1500 is
// a deliberately conservative round figure (≈ a megapixel-class image on
// common encoders) so image-heavy histories trigger compaction early rather
// than blowing the real window.
const imageTokenEstimate = 1500

// EstimateTokens returns a rough token estimate for a conversation. It is a
// cheap heuristic (≈4 characters per token over all text content, a flat
// imageTokenEstimate per image block), good enough to decide when to compact;
// exact accounting comes from the backend's usage.
func EstimateTokens(messages []apiclient.Message) int {
	chars := 0
	images := 0
	for _, m := range messages {
		for _, b := range m.Content {
			switch v := b.(type) {
			case apiclient.TextBlock:
				chars += len(v.Text)
			case apiclient.ToolCallBlock:
				chars += len(v.Input) + len(v.Name)
			case apiclient.ToolResultBlock:
				chars += len(v.Content)
			case apiclient.ImageBlock:
				images++
			}
		}
	}
	return chars/4 + images*imageTokenEstimate
}

// EstimateOverhead returns a rough token estimate for the per-request prompt
// overhead the message history does not show: the composed system prompt plus
// the fence tool-instruction block the client renders into it. Both are large
// in practice (the tool block alone spans every schema), so a threshold
// decision that ignored them would overshoot the real context window.
func EstimateOverhead(system string, tools []apiclient.ToolDef) int {
	return (len(system) + apiclient.ToolInstructionSize(tools)) / 4
}

const compactSystem = `You are summarizing a coding-assistant conversation to ` +
	`save context. Produce a concise summary that preserves: the user's goals, ` +
	`key decisions, files read or changed, and any open tasks. Be factual and brief.`

// Compact replaces all but the last keepRecent messages with a single summary
// message produced by the model. If there is nothing worth compacting it
// returns the messages unchanged. keepRecent <= 0 uses DefaultKeepRecent.
func Compact(ctx context.Context, client apiclient.Client, messages []apiclient.Message, keepRecent int) ([]apiclient.Message, error) {
	if keepRecent <= 0 {
		keepRecent = DefaultKeepRecent
	}
	if len(messages) <= keepRecent+1 {
		return messages, nil
	}

	older := messages[:len(messages)-keepRecent]
	recent := messages[len(messages)-keepRecent:]

	summary, err := summarize(ctx, client, older)
	if err != nil {
		return nil, err
	}

	compacted := make([]apiclient.Message, 0, len(recent)+1)
	compacted = append(compacted, apiclient.Message{
		Role: apiclient.RoleUser,
		Content: []apiclient.ContentBlock{
			apiclient.TextBlock{Text: "Summary of the earlier conversation:\n\n" + summary},
		},
	})
	compacted = append(compacted, recent...)
	return compacted, nil
}

// tightCondenser is the deterministic middle tier's filter: much stricter than
// the per-append condenser (which already ran on Bash output as results
// arrived), because everything it touches is old enough to sit behind the
// keep-recent window.
var tightCondenser = condense.New(condense.Config{MaxLines: 30, HeadLines: 15, TailLines: 10})

// CondenseOlder applies the deterministic tool-output condenser to tool
// results in all but the trailing keepRecent messages, returning a new slice
// (the input and its messages are never mutated). It complements the
// per-append condenser: that one only touches configured tools (Bash by
// default) at their original budget, while this pass squeezes every stale tool
// result hard — those results have already served their purpose.
func CondenseOlder(messages []apiclient.Message, keepRecent int) []apiclient.Message {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(messages) <= keepRecent {
		return messages
	}
	out := make([]apiclient.Message, len(messages))
	copy(out, messages)
	for i := range out[:len(out)-keepRecent] {
		changed := false
		blocks := make([]apiclient.ContentBlock, len(out[i].Content))
		copy(blocks, out[i].Content)
		for j, b := range blocks {
			tr, ok := b.(apiclient.ToolResultBlock)
			if !ok {
				continue
			}
			if condensed := tightCondenser.Condense(tr.Content); condensed != tr.Content {
				tr.Content = condensed
				blocks[j] = tr
				changed = true
			}
		}
		if changed {
			out[i].Content = blocks
		}
	}
	return out
}

// Auto is the tiered auto-compaction entry point: it first
// applies the free deterministic tier (CondenseOlder) and returns early if
// that alone brings the estimate under budget; only then does it fall through
// to the LLM summarization tier (Compact) over the already-condensed history.
// budget <= 0 skips the early exit and always summarizes. The LLM tier keeps
// its fail-open contract at the caller (an error keeps the original history).
func Auto(ctx context.Context, client apiclient.Client, messages []apiclient.Message, keepRecent, budget int) ([]apiclient.Message, error) {
	if keepRecent <= 0 {
		keepRecent = DefaultKeepRecent
	}
	condensed := CondenseOlder(messages, keepRecent)
	if budget > 0 && EstimateTokens(condensed) <= budget {
		return condensed, nil
	}
	return Compact(ctx, client, condensed, keepRecent)
}

// summarize asks the model to summarize older and returns the text.
func summarize(ctx context.Context, client apiclient.Client, older []apiclient.Message) (string, error) {
	// Build a fresh slice so we never mutate the caller's backing array (the
	// trailing "recent" messages share it).
	msgs := make([]apiclient.Message, 0, len(older)+1)
	msgs = append(msgs, older...)
	msgs = append(msgs, apiclient.Message{
		Role: apiclient.RoleUser,
		Content: []apiclient.ContentBlock{
			apiclient.TextBlock{Text: "Summarize the conversation so far as instructed."},
		},
	})
	req := apiclient.Request{System: compactSystem, Messages: msgs}

	events, err := client.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}

	var out strings.Builder
	for evt := range events {
		switch v := evt.(type) {
		case apiclient.TextDeltaEvent:
			out.WriteString(v.Text)
		case apiclient.ErrorEvent:
			return "", fmt.Errorf("compact: %w", v.Err)
		}
	}

	summary := strings.TrimSpace(out.String())
	if summary == "" {
		return "", fmt.Errorf("compact: model returned an empty summary")
	}
	return summary, nil
}
