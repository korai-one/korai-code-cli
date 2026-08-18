// korai eval — the opt-in live smoke layer of the agent-loop eval harness.
// The offline scenario layer runs under `go test ./internal/agenteval/`; this
// subcommand drives the same harness against a real inference endpoint.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/agenteval"
	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// evalCmd builds the `korai eval` subcommand. It is skipped (exit 0) unless an
// endpoint is configured, exits non-zero only on misconfiguration — never on
// low scores — and stays wiring-only: the harness lives in internal/agenteval.
func evalCmd() *cobra.Command {
	var (
		endpoint string
		model    string
		out      string
		seed     int
		runs     int
		timeout  time.Duration
	)

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the live agent-loop eval suite against a real inference endpoint",
		Long: "Runs a small set of live scenarios (real model, real tools, temp workdirs, headless)\n" +
			"against the endpoint given by --endpoint or KORAI_EVAL_ENDPOINT — a worker's loopback\n" +
			"OpenAI-compatible URL or an orchestrator (bearer from KORAI_API_KEY). Each run pins a\n" +
			"sampling seed and temperature 0 for reproducibility and is scored by deterministic\n" +
			"checks only. Without an endpoint the suite is skipped. Records are JSONL; low scores\n" +
			"never fail the command — only misconfiguration does.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if endpoint == "" {
				endpoint = strings.TrimSpace(os.Getenv("KORAI_EVAL_ENDPOINT"))
			}
			if endpoint == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "eval skipped: no endpoint (pass --endpoint or set KORAI_EVAL_ENDPOINT)")
				return nil
			}
			if runs < 1 {
				return fmt.Errorf("--runs must be at least 1, got %d", runs)
			}
			setupLogging(false, cmd.ErrOrStderr())

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return runEval(ctx, cmd, endpoint, model, out, seed, runs, timeout)
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "",
		"inference endpoint URL (default: KORAI_EVAL_ENDPOINT; unset skips the suite)")
	cmd.Flags().StringVar(&model, "model", defaultKoraiModel, "model identifier or routing alias")
	cmd.Flags().StringVar(&out, "out", "",
		"write JSONL records to this file (default: records to stdout, summary to stderr)")
	cmd.Flags().IntVar(&seed, "seed", 42, "base sampling seed (run i uses seed+i)")
	cmd.Flags().IntVar(&runs, "runs", 1, "runs per scenario")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "per-run timeout")

	return cmd
}

// runEval wires the endpoint into the harness and prints records + summary.
// With --out, records go to the file and the summary to stdout; otherwise
// records stream to stdout (JSONL, machine-parseable) and the summary to
// stderr — the same split as --print --output-format json.
func runEval(ctx context.Context, cmd *cobra.Command, endpoint, model, out string, seed, runs int, timeout time.Duration) error {
	// The endpoint speaks the OpenAI-compatible HTTP surface on both live
	// backends (worker loopback and orchestrator), so the KoraiClient covers
	// both; a loopback worker needs no bearer (see session assembly).
	client := apiclient.NewKoraiClient(os.Getenv("KORAI_API_KEY"), endpoint, model)

	records := cmd.OutOrStdout()
	summary := cmd.ErrOrStderr()
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return fmt.Errorf("creating --out file: %w", err)
		}
		defer func() { _ = f.Close() }()
		records = f
		summary = cmd.OutOrStdout()
	}

	recs, err := agenteval.RunLive(ctx, agenteval.LiveOptions{
		Client:  client,
		Runs:    runs,
		Seed:    seed,
		Timeout: timeout,
		Records: records,
	})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(summary, agenteval.FormatLiveSummary(recs)); err != nil {
		return err
	}
	return nil
}
