package main

import (
	"os"
	"testing"
)

func TestResolvePromptFromArg(t *testing.T) {
	got, err := resolvePrompt([]string{"  hello world  "})
	if err != nil {
		t.Fatalf("resolvePrompt: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q (trimmed)", got, "hello world")
	}
}

func TestResolvePromptFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = w.WriteString("piped prompt\n")
		_ = w.Close()
	}()

	got, err := resolvePrompt(nil)
	if err != nil {
		t.Fatalf("resolvePrompt: %v", err)
	}
	if got != "piped prompt" {
		t.Errorf("got %q, want %q", got, "piped prompt")
	}
}

// TestResolvePromptArgBeatsStdin confirms a positional argument is used without
// touching stdin.
func TestResolvePromptArgBeatsStdin(t *testing.T) {
	got, err := resolvePrompt([]string{"from arg"})
	if err != nil {
		t.Fatalf("resolvePrompt: %v", err)
	}
	if got != "from arg" {
		t.Errorf("got %q, want %q", got, "from arg")
	}
}
