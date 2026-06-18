// Command korai is the entry point for Korai Code CLI.
// This file contains only Cobra wiring; no business logic lives here.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tui"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runOptions holds the resolved CLI flags for a run. The *Set fields record
// whether the user passed the flag explicitly, so config-file values can fill
// in the rest.
type runOptions struct {
	prompt      string
	model       string
	modelSet    bool
	permMode    perm.Mode
	permModeSet bool
	autoYes     bool
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
			mode, err := perm.ParseMode(permModeStr)
			if err != nil {
				return err
			}
			opts := runOptions{
				prompt:      printPrompt,
				model:       model,
				modelSet:    cmd.Flags().Changed("model"),
				permMode:    mode,
				permModeSet: cmd.Flags().Changed("permission-mode"),
				autoYes:     autoYes,
			}
			if printPrompt != "" {
				setupLogging(debug, os.Stderr)
				return runPrint(cmd.Context(), opts)
			}
			// The TUI owns the screen: stderr logging would corrupt it, so logs
			// are discarded unless --debug routes them to a file.
			logTarget := io.Discard
			if debug {
				f, ferr := os.CreateTemp("", "korai-*.log")
				if ferr == nil {
					defer func() { _ = f.Close() }()
					logTarget = f
				}
			}
			setupLogging(debug, logTarget)
			return runTUI(cmd.Context(), opts)
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

// setupLogging configures slog to write structured logs to w. At default level
// only warnings and errors are shown so they don't pollute output.
func setupLogging(debug bool, w io.Writer) {
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}

// runPrint drives the engine in headless mode, printing streamed text to stdout.
// SIGINT/SIGTERM cancel the context so an in-flight turn stops cleanly.
func runPrint(ctx context.Context, opts runOptions) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sess, err := assemble(ctx, opts)
	if err != nil {
		return err
	}
	defer sess.close()

	// Headless runs have no interactive prompt: an "ask" decision is denied by
	// default (safe), or auto-approved when --yes is set.
	var asker perm.Asker = perm.DenyAsker{}
	if opts.autoYes {
		asker = perm.AllowAsker{}
	}
	permEngine := perm.NewEngine(sess.mode, sess.rules, asker)

	eng := engine.New(sess.client, sess.registry, permEngine, sess.deps)
	messages := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: opts.prompt}}},
	}

	slog.Debug("starting headless turn", "permission_mode", sess.mode.String())

	for evt := range eng.Run(ctx, messages, sess.system) {
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

// runTUI launches the interactive Bubble Tea REPL. Permission prompts are
// resolved interactively by the TUI's own Asker, so --yes does not apply here.
func runTUI(ctx context.Context, opts runOptions) error {
	sess, err := assemble(ctx, opts)
	if err != nil {
		return err
	}
	defer sess.close()

	asker := tui.NewAsker()
	permEngine := perm.NewEngine(sess.mode, sess.rules, asker)
	eng := engine.New(sess.client, sess.registry, permEngine, sess.deps)

	model := tui.New(eng, asker, sess.system)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
