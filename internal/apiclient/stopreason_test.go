package apiclient_test

import (
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

func TestNormalizeStopReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
	}{
		// The bug this fixes: OpenAI-compatible backends report "length" for a
		// token-limit truncation, which must map to the canonical StopMaxTokens
		// the engine's truncation guard checks.
		{"length", apiclient.StopMaxTokens},
		{"max_tokens", apiclient.StopMaxTokens},
		{"MAX_OUTPUT_TOKENS", apiclient.StopMaxTokens},
		{"stop", apiclient.StopEndTurn},
		{"", apiclient.StopEndTurn},
		{"end_turn", apiclient.StopEndTurn},
		{"tool_calls", apiclient.StopToolUse},
		{"tool_use", apiclient.StopToolUse},
		{"cancelled", apiclient.StopCanceled},
		{"canceled", apiclient.StopCanceled},
		{"  Length  ", apiclient.StopMaxTokens}, // trimmed + lowercased
		{"content_filter", "content_filter"},    // unknown passes through lowercased
	}

	for _, tc := range cases {
		if got := apiclient.NormalizeStopReason(tc.raw); got != tc.want {
			t.Errorf("NormalizeStopReason(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
