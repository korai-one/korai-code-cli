package perm_test

import (
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
)

func TestModeRoundTrip(t *testing.T) {
	t.Parallel()

	modes := []perm.Mode{
		perm.ModeDefault,
		perm.ModePlan,
		perm.ModeAcceptEdits,
		perm.ModeBypassPermissions,
	}
	for _, m := range modes {
		got, err := perm.ParseMode(m.String())
		if err != nil {
			t.Errorf("ParseMode(%q): %v", m.String(), err)
			continue
		}
		if got != m {
			t.Errorf("round trip: got %v, want %v", got, m)
		}
	}
}

func TestParseModeUnknown(t *testing.T) {
	t.Parallel()
	if _, err := perm.ParseMode("nonsense"); err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestRulesMatching(t *testing.T) {
	t.Parallel()

	r := perm.Rules{Allow: []string{"ReadFile", "Grep"}, Deny: []string{"Bash"}}
	if !r.AllowsTool("ReadFile") {
		t.Error("ReadFile should be allowed")
	}
	if r.AllowsTool("Write") {
		t.Error("Write should not be allowed")
	}
	if !r.DeniesTool("Bash") {
		t.Error("Bash should be denied")
	}
	if r.DeniesTool("ReadFile") {
		t.Error("ReadFile should not be denied")
	}

	wild := perm.Rules{Allow: []string{"*"}}
	if !wild.AllowsTool("AnyTool") {
		t.Error("wildcard should allow any tool")
	}
}
