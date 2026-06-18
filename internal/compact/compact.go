// Package compact summarizes older conversation turns to reclaim context. It
// talks to the model only through apiclient.Client, so it is independent of the
// inference backend — the Korai SDK swap requires no change here.
package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// DefaultKeepRecent is how many trailing messages are preserved verbatim.
const DefaultKeepRecent = 4

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
