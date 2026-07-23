package agenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// liveSystemPrompt is the minimal, fixed system prompt for live scenarios. It
// deliberately avoids the full session prompt (git status, project docs, date)
// so runs stay as reproducible as a live model allows.
const liveSystemPrompt = "You are Korai Code, an AI coding agent. Work inside the current directory " +
	"using the available tools. Complete the requested task exactly, then reply with a short confirmation."

// defaultLiveMaxTurns bounds a live scenario's tool loop; the wrap-up path
// (never a hard abort) triggers past it.
const defaultLiveMaxTurns = 8

// LiveScenario is one live smoke case: a real model drives the real engine and
// real tools in a temp workdir, and Check scores the outcome deterministically.
type LiveScenario struct {
	// Name identifies the scenario in records and the summary.
	Name string
	// Prompt is the user task.
	Prompt string
	// Files are fixture files written into the workdir before the run.
	Files map[string]string
	// Tools returns the fresh tool set for a run.
	Tools func() []tool.Tool
	// MaxTurns caps tool iterations (0 = defaultLiveMaxTurns).
	MaxTurns int
	// Check scores the run from the workdir contents and the model's emitted
	// text. A nil error is a pass; the error message is the failure reason.
	Check func(workDir, finalText string) error
}

// LiveOptions configures a live suite run.
type LiveOptions struct {
	// Client is the real inference backend (constructed by the caller — the
	// harness never builds network clients itself).
	Client apiclient.Client
	// Scenarios to run; nil means BuiltinLiveScenarios().
	Scenarios []LiveScenario
	// Runs is how many times each scenario runs (0 = 1). Run i uses seed
	// Seed+i, so a multi-run suite measures robustness across seeds while
	// staying reproducible across invocations.
	Runs int
	// Seed is the base sampling seed pinned on every request (with
	// temperature 0) for reproducibility on backends that honor it.
	Seed int
	// Timeout bounds one scenario run (0 = 3 minutes).
	Timeout time.Duration
	// Records receives one compact JSON object per run (JSONL); nil discards.
	Records io.Writer
}

// LiveRecord is the JSONL record of one live scenario run.
type LiveRecord struct {
	Scenario     string `json:"scenario"`
	Run          int    `json:"run"`
	Seed         int    `json:"seed"`
	Pass         bool   `json:"pass"`
	Fail         string `json:"fail,omitempty"`
	Err          string `json:"error,omitempty"`
	ModelCalls   int    `json:"model_calls"`
	ToolCalls    int    `json:"tool_calls"`
	ToolErrors   int    `json:"tool_errors"`
	FenceRetries int    `json:"fence_retries"`
	WrapUps      int    `json:"wrap_ups"`
	DurationMS   int64  `json:"duration_ms"`
}

// RunLive executes the live suite: every scenario × run in its own temp
// workdir, headless, permission-bypassed (the workdir is disposable), with a
// per-run timeout and a pinned seed. It returns all records; the returned
// error covers harness misconfiguration only — a failing or erroring scenario
// is recorded, never fatal, so callers exit zero on low scores by design.
func RunLive(ctx context.Context, opts LiveOptions) ([]LiveRecord, error) {
	if opts.Client == nil {
		return nil, errors.New("agenteval: live run needs a client")
	}
	scenarios := opts.Scenarios
	if scenarios == nil {
		scenarios = BuiltinLiveScenarios()
	}
	runs := opts.Runs
	if runs <= 0 {
		runs = 1
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}

	var records []LiveRecord
	for _, sc := range scenarios {
		for run := 0; run < runs; run++ {
			if ctx.Err() != nil {
				return records, ctx.Err()
			}
			seed := opts.Seed + run
			rec := runLiveOnce(ctx, opts.Client, sc, run, seed, timeout)
			records = append(records, rec)
			if opts.Records != nil {
				line, err := json.Marshal(rec)
				if err != nil {
					return records, fmt.Errorf("agenteval: encoding record: %w", err)
				}
				if _, err := opts.Records.Write(append(line, '\n')); err != nil {
					return records, fmt.Errorf("agenteval: writing record: %w", err)
				}
			}
		}
	}
	return records, nil
}

// runLiveOnce executes one scenario run in a fresh temp workdir and scores it.
func runLiveOnce(ctx context.Context, client apiclient.Client, sc LiveScenario, run, seed int, timeout time.Duration) LiveRecord {
	rec := LiveRecord{Scenario: sc.Name, Run: run, Seed: seed}
	start := time.Now()
	defer func() { rec.DurationMS = time.Since(start).Milliseconds() }()

	workDir, err := os.MkdirTemp("", "korai-eval-*")
	if err != nil {
		rec.Err = fmt.Sprintf("creating workdir: %v", err)
		return rec
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	if err := writeFixtures(workDir, sc.Files); err != nil {
		rec.Err = err.Error()
		return rec
	}

	registry := tool.NewRegistry()
	for _, t := range sc.Tools() {
		registry.Register(t)
	}
	maxTurns := sc.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultLiveMaxTurns
	}
	// Bypass permissions: the workdir is disposable and headless prompts would
	// otherwise deny every mutating call.
	permEngine := perm.NewEngine(perm.NewModeSelector(perm.ModeBypassPermissions), perm.Rules{}, perm.DenyAsker{})
	recClient := &recordingClient{inner: client}
	temp := 0.0
	eng := engine.New(recClient, registry, permEngine, tool.Deps{WorkDir: workDir},
		engine.WithMaxToolTurns(maxTurns),
		engine.WithSamplingDefaults(apiclient.Sampling{Seed: &seed, Temperature: &temp}),
	)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	col := newCollector(workDir)
	for evt := range eng.Run(runCtx, []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: sc.Prompt}}},
	}, liveSystemPrompt) {
		col.observe(evt)
	}
	res := col.finish(recClient.requests())

	rec.ModelCalls = res.Metrics.ModelCalls
	rec.ToolCalls = res.Metrics.ToolCalls
	rec.ToolErrors = res.Metrics.ToolErrors
	rec.FenceRetries = res.Metrics.FenceRetries
	rec.WrapUps = res.Metrics.WrapUps
	if res.Err != nil {
		rec.Err = res.Err.Error()
		return rec
	}
	if sc.Check != nil {
		if err := sc.Check(workDir, res.FinalText); err != nil {
			rec.Fail = err.Error()
			return rec
		}
	}
	rec.Pass = true
	return rec
}
