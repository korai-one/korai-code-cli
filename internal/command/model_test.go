package command_test

import (
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/command"
)

// fakeSelector is an in-memory command.Selector for tests.
type fakeSelector struct{ model string }

func (s *fakeSelector) Get() string      { return s.model }
func (s *fakeSelector) Set(model string) { s.model = model }

func TestModelListNoArgs(t *testing.T) {
	t.Parallel()
	sel := &fakeSelector{model: "claude-sonnet-4-6"}
	cmd := command.NewModelCommand([]string{"claude-opus-4-8", "claude-sonnet-4-6"}, sel)

	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Action != command.ShowText {
		t.Errorf("action = %v, want ShowText", res.Action)
	}
	if !strings.Contains(res.Text, "claude-opus-4-8") || !strings.Contains(res.Text, "claude-sonnet-4-6") {
		t.Errorf("listing missing models:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "* claude-sonnet-4-6") {
		t.Errorf("current model not marked:\n%s", res.Text)
	}
	if sel.model != "claude-sonnet-4-6" {
		t.Error("listing must not change the model")
	}
}

func TestModelSwitch(t *testing.T) {
	t.Parallel()
	sel := &fakeSelector{model: "claude-sonnet-4-6"}
	cmd := command.NewModelCommand([]string{"claude-opus-4-8", "claude-sonnet-4-6"}, sel)

	res, err := cmd.Run("claude-opus-4-8")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sel.model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", sel.model)
	}
	if !strings.Contains(res.Text, "switched") {
		t.Errorf("expected confirmation, got %q", res.Text)
	}
}

func TestModelSwitchUnknown(t *testing.T) {
	t.Parallel()
	sel := &fakeSelector{model: "claude-sonnet-4-6"}
	cmd := command.NewModelCommand([]string{"claude-opus-4-8", "claude-sonnet-4-6"}, sel)

	res, err := cmd.Run("gpt-4")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sel.model != "claude-sonnet-4-6" {
		t.Error("unknown model must not change the selection")
	}
	if !strings.Contains(res.Text, "unknown model") {
		t.Errorf("expected unknown-model message, got %q", res.Text)
	}
}

func TestModelMetadata(t *testing.T) {
	t.Parallel()
	cmd := command.NewModelCommand(nil, &fakeSelector{})
	if cmd.Name() != "model" {
		t.Errorf("name = %q, want model", cmd.Name())
	}
	if cmd.Description() == "" {
		t.Error("description should not be empty")
	}
}
