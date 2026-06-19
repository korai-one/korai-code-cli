package command_test

import (
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/command"
)

func TestCostCommand(t *testing.T) {
	t.Parallel()
	cmd := command.NewCostCommand(func() string { return "usage: 42 tokens" })
	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Action != command.ShowText || !strings.Contains(res.Text, "42 tokens") {
		t.Errorf("result = %+v", res)
	}
	if cmd.Name() != "cost" {
		t.Errorf("name = %q, want cost", cmd.Name())
	}
}

func TestAboutCommand(t *testing.T) {
	t.Parallel()
	cmd := command.NewAboutCommand("Korai Code CLI 1.2.3")
	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Action != command.ShowText || !strings.Contains(res.Text, "1.2.3") {
		t.Errorf("result = %+v", res)
	}
	if cmd.Name() != "about" {
		t.Errorf("name = %q, want about", cmd.Name())
	}
}

func TestCompactCommand(t *testing.T) {
	t.Parallel()
	cmd := command.NewCompactCommand()
	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Action != command.CompactHistory {
		t.Errorf("action = %v, want CompactHistory", res.Action)
	}
	if cmd.Name() != "compact" {
		t.Errorf("name = %q, want compact", cmd.Name())
	}
}

func TestPlanCommand(t *testing.T) {
	t.Parallel()
	state := "default"
	toggle := func() string {
		if state == "plan" {
			state = "default"
		} else {
			state = "plan"
		}
		return state
	}
	cmd := command.NewPlanCommand(toggle)
	if cmd.Name() != "plan" {
		t.Errorf("name = %q, want plan", cmd.Name())
	}

	res, _ := cmd.Run("")
	if res.Action != command.ShowText || !strings.Contains(res.Text, "plan") {
		t.Errorf("first toggle = %+v, want plan", res)
	}
	res, _ = cmd.Run("")
	if !strings.Contains(res.Text, "default") {
		t.Errorf("second toggle = %+v, want default", res)
	}
}

func TestResumeCommand(t *testing.T) {
	t.Parallel()
	cmd := command.NewResumeCommand(func() string { return "session list here" })
	if cmd.Name() != "resume" {
		t.Errorf("name = %q, want resume", cmd.Name())
	}

	res, _ := cmd.Run("")
	if res.Action != command.ShowText || !strings.Contains(res.Text, "session list") {
		t.Errorf("no-arg = %+v, want list", res)
	}

	res, _ = cmd.Run("abc-123")
	if res.Action != command.ResumeSession || res.Text != "abc-123" {
		t.Errorf("with-id = %+v, want ResumeSession(abc-123)", res)
	}
}
