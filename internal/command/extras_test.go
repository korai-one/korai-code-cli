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
