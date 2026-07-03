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
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/compact"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tui"
)

// version is the Korai CLI version, shown by --version and the TUI welcome
// banner. Bump on release; the release build overrides it via
// -ldflags "-X main.version=<tag>" (which requires a var, not a const).
var version = "0.1.0"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// resolvePrompt returns the headless prompt: the positional argument if given,
// otherwise stdin when it is piped or redirected (not an interactive terminal).
func resolvePrompt(args []string) (string, error) {
	if len(args) > 0 {
		return strings.TrimSpace(args[0]), nil
	}
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		// No data piped in (stdin is a terminal); nothing to read.
		return "", nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
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
	cont        bool
	resumeID    string
	// local forces local-worker inference: the session must resolve a co-located
	// or LAN worker (else startup fails), and no remote API key is required.
	local bool
	// localWorkerURL, when set, routes inference straight to a loopback Korai
	// worker (bypassing the orchestrator). Empty means auto-detect an advertised
	// worker, then fall back to the networked backend.
	localWorkerURL string
	// localWorkerAddr, when set, routes inference to a home/LAN inference server
	// over the direct binary channel on TCP (host:port); the token comes from
	// KORAI_LOCAL_WORKER_TOKEN.
	localWorkerAddr string
}

func rootCmd() *cobra.Command {
	var (
		printMode       bool
		model           string
		debug           bool
		permModeStr     string
		autoYes         bool
		cont            bool
		resumeID        string
		local           bool
		localWorker     string
		localWorkerAddr string
	)

	root := &cobra.Command{
		Use:     "korai [prompt]",
		Version: version,
		Short:   "Korai Code CLI — an AI coding agent on the Korai P2P inference network",
		Long: "Korai Code CLI — an AI coding agent on the Korai P2P inference network.\n\n" +
			"Starts an interactive session by default. Use -p/--print for non-interactive\n" +
			"output, with the prompt as an argument or piped on stdin.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := perm.ParseMode(permModeStr)
			if err != nil {
				return err
			}
			opts := runOptions{
				model:           model,
				modelSet:        cmd.Flags().Changed("model"),
				permMode:        mode,
				permModeSet:     cmd.Flags().Changed("permission-mode"),
				autoYes:         autoYes,
				cont:            cont,
				resumeID:        resumeID,
				local:           local,
				localWorkerURL:  localWorker,
				localWorkerAddr: localWorkerAddr,
			}
			if printMode {
				prompt, perr := resolvePrompt(args)
				if perr != nil {
					return perr
				}
				if prompt == "" {
					return fmt.Errorf("no prompt: pass one as an argument or pipe it on stdin with -p")
				}
				opts.prompt = prompt
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

	root.Flags().BoolVarP(&printMode, "print", "p", false,
		"run a single prompt (from the argument or stdin) and exit")
	root.Flags().StringVar(&model, "model", "", "model identifier (default: the backend's default — auto for Korai)")
	root.Flags().BoolVar(&debug, "debug", false, "enable debug logging to stderr")
	root.Flags().StringVar(&permModeStr, "permission-mode", "default",
		"permission mode: default | plan | acceptEdits | bypassPermissions")
	root.Flags().BoolVar(&autoYes, "yes", false,
		"auto-approve prompts that would otherwise be denied in headless mode")
	root.Flags().BoolVarP(&cont, "continue", "c", false,
		"resume the most recent session in this directory")
	root.Flags().StringVar(&resumeID, "resume", "",
		"resume a saved session by id")
	root.Flags().BoolVar(&local, "local", false,
		"require a local/LAN worker for inference and run without any API key")
	root.Flags().StringVar(&localWorker, "local-worker-url", "",
		"route inference to a local Korai worker at this URL (default: auto-detect, else use the network)")
	root.Flags().StringVar(&localWorkerAddr, "local-worker-addr", "",
		"route inference to a home/LAN inference server over the direct binary channel (host:port; token via KORAI_LOCAL_WORKER_TOKEN)")

	root.AddCommand(serveCmd())

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

	sess, err := assemble(ctx, opts, headlessPlanApprover{autoYes: opts.autoYes})
	if err != nil {
		return err
	}
	defer sess.close()

	// Auth is gated at the prompt, not at startup: a keyless session can be
	// assembled, but a remote turn needs a key. Fail before the turn with a clear
	// message rather than mid-stream.
	if gerr := sess.authGate(); gerr != nil {
		return gerr
	}

	// Headless runs have no interactive prompt: an "ask" decision is denied by
	// default (safe), or auto-approved when --yes is set.
	var asker perm.Asker = perm.DenyAsker{}
	if opts.autoYes {
		asker = perm.AllowAsker{}
	}
	permEngine := perm.NewEngine(sess.modes, sess.rules, asker)

	eng := engine.New(sess.client, sess.registry, permEngine, sess.deps,
		engine.WithHooks(sess.hooks), engine.WithModelSelector(sess.models),
		engine.WithUsageRecorder(sess.cost.Add), engine.WithSystemSuffix(planSuffix(sess.modes)),
		engine.WithAutoCompact(compact.DefaultThreshold, compact.EstimateTokens, sess.compactor))
	// Continue from any resumed history, then add this prompt.
	messages := make([]apiclient.Message, 0, len(sess.initialHistory)+1)
	messages = append(messages, sess.initialHistory...)
	messages = append(messages, apiclient.Message{
		Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: opts.prompt}},
	})

	slog.Debug("starting headless turn", "permission_mode", sess.modes.Get().String())

	var finalHistory []apiclient.Message
	for evt := range eng.Run(ctx, messages, sess.system) {
		switch v := evt.(type) {
		case engine.TextEvent:
			fmt.Print(v.Text)
		case engine.CompactedEvent:
			fmt.Fprintf(os.Stderr, "[auto-compacted: %d → %d messages]\n", v.Before, v.After)
		case engine.ToolStartEvent:
			fmt.Fprintf(os.Stderr, "\n[tool: %s]\n", v.Name)
		case engine.ToolResultEvent:
			if v.Result.IsError {
				fmt.Fprintf(os.Stderr, "[tool error: %s]\n", v.Result.Content)
			}
		case engine.DoneEvent:
			finalHistory = v.Messages
		case engine.ErrorEvent:
			return v.Err
		}
	}
	fmt.Println()
	if finalHistory != nil {
		sess.saver(sess.sessionID, sess.sessionStart, finalHistory)
	}
	return nil
}

// runTUI launches the interactive Bubble Tea REPL. Permission prompts are
// resolved interactively by the TUI's own Asker, so --yes does not apply here.
func runTUI(ctx context.Context, opts runOptions) error {
	planApprover := tui.NewPlanApprover()
	sess, err := assemble(ctx, opts, planApprover)
	if err != nil {
		return err
	}
	defer sess.close()

	asker := tui.NewAsker()
	permEngine := perm.NewEngine(sess.modes, sess.rules, asker)
	eng := engine.New(sess.client, sess.registry, permEngine, sess.deps,
		engine.WithHooks(sess.hooks), engine.WithModelSelector(sess.models),
		engine.WithUsageRecorder(sess.cost.Add), engine.WithSystemSuffix(planSuffix(sess.modes)),
		engine.WithAutoCompact(compact.DefaultThreshold, compact.EstimateTokens, sess.compactor))

	model := tui.New(eng, asker, sess.system, sess.commands).
		WithVersion(version).
		WithCompactor(sess.compactor).WithModes(sess.modes).WithPlanApprover(planApprover).
		WithModels(sess.models).WithCost(sess.cost).WithAuthGate(sess.authGate).
		WithFileFinder(sess.fileFinder).WithMentionExpander(sess.mentionExpander).
		WithImageAttacher(sess.imageAttacher).
		WithSaver(sess.saver).WithResumeLoader(sess.resumeLoad).
		WithSnapshotter(sess.snapshots, sess.snaplog).
		WithSession(sess.sessionID, sess.sessionStart, sess.initialHistory)
	// v2: alt-screen + mouse mode are requested per-frame on the Model's View,
	// not as NewProgram options.
	p := tea.NewProgram(model, tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
