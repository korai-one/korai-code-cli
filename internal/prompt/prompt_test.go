package prompt_test

import (
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/prompt"
)

func TestComposeWithoutContext(t *testing.T) {
	t.Parallel()

	got := prompt.Compose("")
	if !strings.Contains(got, "You are Korai") {
		t.Errorf("missing agent identity in prompt:\n%s", got)
	}
	if strings.Contains(got, "# Environment") {
		t.Errorf("empty context should not produce an Environment section:\n%s", got)
	}
}

func TestComposeHasBehaviorSections(t *testing.T) {
	t.Parallel()

	got := prompt.Compose("")
	for _, want := range []string{
		"# How you operate",
		"# Doing tasks",
		"# Acting with care",
		"# Using your tools",
		"# Style",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing section %q", want)
		}
	}
}

func TestComposeWithContext(t *testing.T) {
	t.Parallel()

	got := prompt.Compose("Working directory: /tmp")
	if !strings.Contains(got, "You are Korai") {
		t.Errorf("missing agent identity:\n%s", got)
	}
	if !strings.Contains(got, "# Environment") {
		t.Errorf("missing Environment section:\n%s", got)
	}
	if !strings.Contains(got, "Working directory: /tmp") {
		t.Errorf("missing context body:\n%s", got)
	}
}
