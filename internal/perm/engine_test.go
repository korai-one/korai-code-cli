package perm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mode  perm.Mode
		rules perm.Rules
		asker perm.Asker
		req   perm.Request
		want  perm.Outcome
	}{
		{
			name:  "bypass short-circuits even with deny rule",
			mode:  perm.ModeBypassPermissions,
			rules: perm.Rules{Deny: []string{"Bash"}},
			asker: perm.DenyAsker{},
			req:   perm.Request{ToolName: "Bash", Base: perm.DecisionDeny},
			want:  perm.OutcomeAllowed,
		},
		{
			name:  "deny rule overrides base allow",
			mode:  perm.ModeDefault,
			rules: perm.Rules{Deny: []string{"Bash"}},
			asker: perm.AllowAsker{},
			req:   perm.Request{ToolName: "Bash", Base: perm.DecisionAllow},
			want:  perm.OutcomeDenied,
		},
		{
			name:  "base deny is denied",
			mode:  perm.ModeDefault,
			asker: perm.AllowAsker{},
			req:   perm.Request{ToolName: "Write", Base: perm.DecisionDeny},
			want:  perm.OutcomeDenied,
		},
		{
			name:  "base allow is allowed",
			mode:  perm.ModeDefault,
			asker: perm.DenyAsker{},
			req:   perm.Request{ToolName: "ReadFile", Base: perm.DecisionAllow},
			want:  perm.OutcomeAllowed,
		},
		{
			name:  "ask with allow rule is allowed",
			mode:  perm.ModeDefault,
			rules: perm.Rules{Allow: []string{"Bash"}},
			asker: perm.DenyAsker{},
			req:   perm.Request{ToolName: "Bash", Base: perm.DecisionAsk},
			want:  perm.OutcomeAllowed,
		},
		{
			name:  "ask without rule defers to deny asker",
			mode:  perm.ModeDefault,
			asker: perm.DenyAsker{},
			req:   perm.Request{ToolName: "Bash", Base: perm.DecisionAsk},
			want:  perm.OutcomeDenied,
		},
		{
			name:  "ask without rule defers to allow asker",
			mode:  perm.ModeDefault,
			asker: perm.AllowAsker{},
			req:   perm.Request{ToolName: "Bash", Base: perm.DecisionAsk},
			want:  perm.OutcomeAllowed,
		},
		{
			name:  "allow rule does not override base deny",
			mode:  perm.ModeDefault,
			rules: perm.Rules{Allow: []string{"Write"}},
			asker: perm.AllowAsker{},
			req:   perm.Request{ToolName: "Write", Base: perm.DecisionDeny},
			want:  perm.OutcomeDenied,
		},
		{
			name:  "wildcard allow rule matches any tool on ask",
			mode:  perm.ModeDefault,
			rules: perm.Rules{Allow: []string{"*"}},
			asker: perm.DenyAsker{},
			req:   perm.Request{ToolName: "Bash", Base: perm.DecisionAsk},
			want:  perm.OutcomeAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := perm.NewEngine(tt.mode, tt.rules, tt.asker)
			got, err := e.Resolve(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tt.want {
				t.Errorf("outcome = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveAskerError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("prompt failed")
	asker := perm.AskFunc(func(context.Context, perm.Request) (perm.Decision, error) {
		return perm.DecisionAllow, sentinel
	})
	e := perm.NewEngine(perm.ModeDefault, perm.Rules{}, asker)

	got, err := e.Resolve(context.Background(), perm.Request{ToolName: "Bash", Base: perm.DecisionAsk})
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want %v", err, sentinel)
	}
	if got != perm.OutcomeDenied {
		t.Errorf("outcome = %v, want denied on asker error (fail-closed)", got)
	}
}

func TestNewEngineNilAskerFailsClosed(t *testing.T) {
	t.Parallel()

	e := perm.NewEngine(perm.ModeDefault, perm.Rules{}, nil)
	got, err := e.Resolve(context.Background(), perm.Request{ToolName: "Bash", Base: perm.DecisionAsk})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != perm.OutcomeDenied {
		t.Errorf("outcome = %v, want denied (nil asker should default to DenyAsker)", got)
	}
}

func TestMode(t *testing.T) {
	t.Parallel()
	e := perm.NewEngine(perm.ModeAcceptEdits, perm.Rules{}, nil)
	if e.Mode() != perm.ModeAcceptEdits {
		t.Errorf("Mode() = %v, want acceptEdits", e.Mode())
	}
}
