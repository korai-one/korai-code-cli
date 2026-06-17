// Command korai is the entry point for Korai Code CLI.
// This file contains only Cobra wiring; no business logic lives here.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	appctx "github.com/Nevaero/korai-code-cli/internal/context"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/prompt"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		printPrompt string
		model       string
		debug       bool
	)

	root := &cobra.Command{
		Use:           "korai",
		Short:         "Korai Code CLI — an AI coding agent on the Korai P2P inference network",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogging(debug)
			if printPrompt != "" {
				return runPrint(cmd.Context(), printPrompt, model)
			}
			// TODO(phase4): launch interactive TUI
			return cmd.Help()
		},
	}

	root.Flags().StringVar(&printPrompt, "print", "", "run a single prompt in headless mode and exit")
	root.Flags().StringVar(&model, "model", "claude-sonnet-4-6", "model identifier")
	root.Flags().BoolVar(&debug, "debug", false, "enable debug logging to stderr")

	return root
}

// setupLogging configures slog to write structured logs to stderr. At default
// level only warnings and errors are shown so they don't pollute --print output.
func setupLogging(debug bool) {
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}

// runPrint drives the engine in headless mode, printing streamed text to stdout.
// SIGINT/SIGTERM cancel the context so an in-flight turn stops cleanly.
func runPrint(ctx context.Context, userPrompt, model string) error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := apiclient.NewAnthropicClient(apiKey, model)
	registry := tool.NewRegistry()
	registry.Register(readfile.New())

	eng := engine.New(client, registry, perm.ModeDefault, tool.Deps{WorkDir: wd})
	system := prompt.Compose(appctx.Build(ctx, wd))
	messages := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: userPrompt}}},
	}

	slog.Debug("starting headless turn", "model", model, "workdir", wd)

	for evt := range eng.Run(ctx, messages, system) {
		switch v := evt.(type) {
		case engine.TextEvent:
			fmt.Print(v.Text)
		case engine.ToolStartEvent:
			fmt.Fprintf(os.Stderr, "\n[tool: %s]\n", v.Name)
		case engine.ToolResultEvent:
			if v.Result.IsError {
				fmt.Fprintf(os.Stderr, "[tool error: %s]\n", v.Result.Content)
			}
		case engine.ErrorEvent:
			return v.Err
		}
	}
	fmt.Println()
	return nil
}
