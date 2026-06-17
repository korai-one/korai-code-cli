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
	"github.com/Nevaero/korai-code-cli/internal/tools"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runOptions holds the resolved CLI flags for a headless run.
type runOptions struct {
	prompt   string
	model    string
	permMode perm.Mode
	autoYes  bool
}

func rootCmd() *cobra.Command {
	var (
		printPrompt string
		model       string
		debug       bool
		permModeStr string
		autoYes     bool
	)

	root := &cobra.Command{
		Use:           "korai",
		Short:         "Korai Code CLI — an AI coding agent on the Korai P2P inference network",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			setupLogging(debug)
			mode, err := perm.ParseMode(permModeStr)
			if err != nil {
				return err
			}
			if printPrompt != "" {
				return runPrint(cmd.Context(), runOptions{
					prompt:   printPrompt,
					model:    model,
					permMode: mode,
					autoYes:  autoYes,
				})
			}
			// TODO(phase4): launch interactive TUI
			return cmd.Help()
		},
	}

	root.Flags().StringVar(&printPrompt, "print", "", "run a single prompt in headless mode and exit")
	root.Flags().StringVar(&model, "model", "claude-sonnet-4-6", "model identifier")
	root.Flags().BoolVar(&debug, "debug", false, "enable debug logging to stderr")
	root.Flags().StringVar(&permModeStr, "permission-mode", "default",
		"permission mode: default | plan | acceptEdits | bypassPermissions")
	root.Flags().BoolVar(&autoYes, "yes", false,
		"auto-approve prompts that would otherwise be denied in headless mode")

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
func runPrint(ctx context.Context, opts runOptions) error {
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

	client := apiclient.NewAnthropicClient(apiKey, opts.model)
	registry := tool.NewRegistry()
	tools.RegisterAll(registry)

	// Headless runs have no interactive prompt: an "ask" decision is denied by
	// default (safe), or auto-approved when --yes is set.
	var asker perm.Asker = perm.DenyAsker{}
	if opts.autoYes {
		asker = perm.AllowAsker{}
	}
	permEngine := perm.NewEngine(opts.permMode, perm.Rules{}, asker)

	eng := engine.New(client, registry, permEngine, tool.Deps{WorkDir: wd})
	system := prompt.Compose(appctx.Build(ctx, wd))
	messages := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: opts.prompt}}},
	}

	slog.Debug("starting headless turn", "model", opts.model, "workdir", wd, "permission_mode", opts.permMode.String())

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
