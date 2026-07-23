package agenteval_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/agenteval"
	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/compact"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	memstore "github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/edit"
	memtool "github.com/Nevaero/korai-code-cli/internal/tools/memory"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
)

var update = flag.Bool("update", false, "update golden transcript files")

// expect is the deterministic score card for one scenario.
type expect struct {
	// seq is the exact sequence of executed tools.
	seq []string
	// files maps workdir-relative paths to their exact expected final contents.
	files map[string]string
	// metrics is the full expected tally (zero fields assert zero).
	metrics agenteval.Metrics
	// finalContains must appear in the run's emitted text.
	finalContains string
	// extra runs scenario-specific assertions on the full Result.
	extra func(t *testing.T, res agenteval.Result)
}

// readEditTools is the Setup for scenarios that only need ReadFile + Edit.
func readEditTools(string) ([]tool.Tool, []engine.Option) {
	return []tool.Tool{readfile.New(), edit.New()}, nil
}

// TestScenarios is the offline eval suite: each case replays a scripted model
// through the real engine and real tools in a temp workdir and scores the run
// deterministically — tool sequence, final files, metrics, golden transcript.
func TestScenarios(t *testing.T) {
	t.Parallel()

	cases := []struct {
		sc   agenteval.Scenario
		want expect
	}{
		{
			// The baseline: read a file, edit it, report. Zero churn expected.
			sc: agenteval.Scenario{
				Name:   "happy_read_edit",
				System: "You are a coding agent.",
				Prompt: "Change world to korai in main.txt",
				Files:  map[string]string{"main.txt": "hello world\n"},
				Mode:   perm.ModeBypassPermissions,
				Setup:  readEditTools,
				Turns: []agenteval.Turn{
					{Text: "Reading the file first.", Calls: []agenteval.Call{{ID: "c1", Name: "ReadFile", Input: `{"path":"main.txt"}`}}},
					{Calls: []agenteval.Call{{ID: "c2", Name: "Edit", Input: `{"path":"main.txt","old_string":"world","new_string":"korai"}`}}},
					{Text: "Replaced world with korai."},
				},
			},
			want: expect{
				seq:   []string{"ReadFile", "Edit"},
				files: map[string]string{"main.txt": "hello korai\n"},
				metrics: agenteval.Metrics{
					ModelCalls: 3, ToolCalls: 2, ToolResults: 2,
				},
				finalContains: "Replaced world with korai.",
			},
		},
		{
			// A turn with an unterminated fence triggers the one-shot retry:
			// the corrective notice is injected, the retry turn is grammar-
			// constrained, and the corrected call then runs normally.
			sc: agenteval.Scenario{
				Name:   "malformed_fence_retry",
				Prompt: "What is in a.txt?",
				Files:  map[string]string{"a.txt": "alpha\n"},
				Mode:   perm.ModeBypassPermissions,
				Setup:  readEditTools,
				Turns: []agenteval.Turn{
					{Text: `Let me read it. <tool:ReadFile>{"path": "a.txt"`},
					{Calls: []agenteval.Call{{ID: "c1", Name: "ReadFile", Input: `{"path":"a.txt"}`}}},
					{Text: "The file contains alpha."},
				},
			},
			want: expect{
				seq:   []string{"ReadFile"},
				files: map[string]string{"a.txt": "alpha\n"},
				metrics: agenteval.Metrics{
					ModelCalls: 3, ToolCalls: 1, ToolResults: 1, FenceRetries: 1,
				},
				finalContains: "The file contains alpha.",
				extra: func(t *testing.T, res agenteval.Result) {
					t.Helper()
					if res.Requests[0].ConstrainTools || !res.Requests[1].ConstrainTools || res.Requests[2].ConstrainTools {
						t.Error("only the retry request (index 1) must set ConstrainTools")
					}
					if notice := lastUser(res.Requests[1]); !strings.Contains(notice, `<tool:ReadFile>{"path": "a.txt"`) {
						t.Errorf("corrective notice does not quote the malformed fence: %q", notice)
					}
				},
			},
		},
		{
			// The loop-detector escalation: identical no-progress calls run
			// silently, then with a warning, then are vetoed twice, forcing the
			// graceful wrap-up.
			sc: agenteval.Scenario{
				Name:   "repeat_warn_veto",
				Prompt: "Check the status until it changes",
				Files:  map[string]string{"status.txt": "state: unchanged\n"},
				Mode:   perm.ModeBypassPermissions,
				Setup:  readEditTools,
				Turns: []agenteval.Turn{
					{Calls: []agenteval.Call{{ID: "c0", Name: "ReadFile", Input: `{"path":"status.txt"}`}}},
					{Calls: []agenteval.Call{{ID: "c1", Name: "ReadFile", Input: `{"path":"status.txt"}`}}},
					{Calls: []agenteval.Call{{ID: "c2", Name: "ReadFile", Input: `{"path":"status.txt"}`}}},
					{Calls: []agenteval.Call{{ID: "c3", Name: "ReadFile", Input: `{"path":"status.txt"}`}}},
					{Text: "The status is unchanged; stopping here."},
				},
			},
			want: expect{
				seq: []string{"ReadFile", "ReadFile"},
				metrics: agenteval.Metrics{
					ModelCalls: 5, ToolCalls: 2, ToolResults: 4, ToolErrors: 2,
					LoopWarnings: 1, Vetoes: 2, WrapUps: 1,
				},
				finalContains: "stopping here",
				extra: func(t *testing.T, res agenteval.Result) {
					t.Helper()
					if last := res.Requests[len(res.Requests)-1]; len(last.Tools) != 0 {
						t.Errorf("wrap-up request still carries %d tools, want none", len(last.Tools))
					}
				},
			},
		},
		{
			// The turn budget: after WithMaxToolTurns iterations of productive,
			// distinct calls, the engine forces a tool-less wrap-up turn.
			sc: agenteval.Scenario{
				Name:   "turn_budget_wrapup",
				Prompt: "Survey every file",
				Files: map[string]string{
					"f0.txt": "zero\n", "f1.txt": "one\n", "f2.txt": "two\n",
				},
				Mode: perm.ModeBypassPermissions,
				Setup: func(string) ([]tool.Tool, []engine.Option) {
					return []tool.Tool{readfile.New()}, []engine.Option{engine.WithMaxToolTurns(3)}
				},
				Turns: []agenteval.Turn{
					{Calls: []agenteval.Call{{ID: "c0", Name: "ReadFile", Input: `{"path":"f0.txt"}`}}},
					{Calls: []agenteval.Call{{ID: "c1", Name: "ReadFile", Input: `{"path":"f1.txt"}`}}},
					{Calls: []agenteval.Call{{ID: "c2", Name: "ReadFile", Input: `{"path":"f2.txt"}`}}},
					{Text: "Budget summary: read three files."},
				},
			},
			want: expect{
				seq: []string{"ReadFile", "ReadFile", "ReadFile"},
				metrics: agenteval.Metrics{
					ModelCalls: 4, ToolCalls: 3, ToolResults: 3, WrapUps: 1,
				},
				finalContains: "Budget summary",
			},
		},
		{
			// The truncation guard: a tool call from a max_tokens-truncated
			// turn is refused (arguments may be cut off); the model retries and
			// the edit lands exactly once.
			sc: agenteval.Scenario{
				Name:   "truncation_guard",
				Prompt: "Change world to korai in main.txt",
				Files:  map[string]string{"main.txt": "hello world\n"},
				Mode:   perm.ModeBypassPermissions,
				Setup:  readEditTools,
				Turns: []agenteval.Turn{
					{
						Calls: []agenteval.Call{{ID: "c1", Name: "Edit", Input: `{"path":"main.txt","old_string":"world","new_string":"korai"}`}},
						Stop:  apiclient.StopMaxTokens,
					},
					{Calls: []agenteval.Call{{ID: "c2", Name: "Edit", Input: `{"path":"main.txt","old_string":"world","new_string":"korai"}`}}},
					{Text: "Edited after the retry."},
				},
			},
			want: expect{
				seq:   []string{"Edit"},
				files: map[string]string{"main.txt": "hello korai\n"},
				metrics: agenteval.Metrics{
					ModelCalls: 3, ToolCalls: 1, ToolResults: 2, ToolErrors: 1, Truncations: 1,
				},
				finalContains: "Edited after the retry.",
			},
		},
		{
			// Intra-turn compaction: a big tool result pushes the estimate past
			// the threshold mid-turn; only the pre-run history is compacted and
			// the current run's tool result survives verbatim.
			sc: agenteval.Scenario{
				Name: "compaction_preserves_run",
				History: []apiclient.Message{
					{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: strings.Repeat("earlier context ", 125)}}},
					{Role: apiclient.RoleAssistant, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "acknowledged, standing by."}}},
				},
				Prompt: "summarize data.txt",
				Files:  map[string]string{"data.txt": strings.Repeat("compaction fixture data line\n", 42)},
				Mode:   perm.ModeBypassPermissions,
				Setup: func(string) ([]tool.Tool, []engine.Option) {
					// Deterministic compactor: collapse whatever it is given to a
					// single marker message (the real path summarizes via the LLM;
					// the engine seam is what is under test here).
					compactFn := func(_ context.Context, msgs []apiclient.Message) ([]apiclient.Message, error) {
						marker := fmt.Sprintf("[compacted %d earlier messages]", len(msgs))
						return []apiclient.Message{{
							Role:    apiclient.RoleUser,
							Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: marker}},
						}}, nil
					}
					return []tool.Tool{readfile.New()},
						[]engine.Option{engine.WithAutoCompact(600, compact.EstimateTokens, compactFn)}
				},
				Turns: []agenteval.Turn{
					{Calls: []agenteval.Call{{ID: "c1", Name: "ReadFile", Input: `{"path":"data.txt"}`}}},
					{Text: "The data is 42 identical lines."},
				},
			},
			want: expect{
				seq: []string{"ReadFile"},
				metrics: agenteval.Metrics{
					ModelCalls: 2, ToolCalls: 1, ToolResults: 1, Compactions: 1,
				},
				finalContains: "42 identical lines",
				extra: func(t *testing.T, res agenteval.Result) {
					t.Helper()
					var sawMarker, sawResult bool
					for _, m := range res.History {
						for _, b := range m.Content {
							switch v := b.(type) {
							case apiclient.TextBlock:
								if strings.Contains(v.Text, "[compacted 2 earlier messages]") {
									sawMarker = true
								}
							case apiclient.ToolResultBlock:
								if strings.Contains(v.Content, "compaction fixture data line") {
									sawResult = true
								}
							}
						}
					}
					if !sawMarker {
						t.Error("history missing the compaction marker for the pre-run messages")
					}
					if !sawResult {
						t.Error("current run's tool result was compacted away — it must survive verbatim")
					}
				},
			},
		},
		{
			// The memory fabric: a Remember write during the tool loop is
			// visible in the system prompt of the very next model call, and the
			// store file lands on disk.
			sc: agenteval.Scenario{
				Name:   "memory_remember_visibility",
				System: "You are a coding agent.",
				Prompt: "Remember where the deploy password lives",
				Mode:   perm.ModeBypassPermissions,
				Setup: func(workDir string) ([]tool.Tool, []engine.Option) {
					store := memstore.NewStore(filepath.Join(workDir, ".korai", "MEMORY.md"))
					return []tool.Tool{memtool.New(store)},
						[]engine.Option{engine.WithSystemSection(memstore.NewProvider(store).Section)}
				},
				Turns: []agenteval.Turn{
					{Calls: []agenteval.Call{{ID: "c1", Name: "Remember", Input: `{"note":"the deploy password lives in the team vault","pinned":true}`}}},
					{Text: "Noted."},
				},
			},
			want: expect{
				seq: []string{"Remember"},
				metrics: agenteval.Metrics{
					ModelCalls: 2, ToolCalls: 1, ToolResults: 1,
				},
				finalContains: "Noted.",
				extra: func(t *testing.T, res agenteval.Result) {
					t.Helper()
					if strings.Contains(res.Requests[0].System, "team vault") {
						t.Error("request 1 already contains the note — nothing had been remembered yet")
					}
					if !strings.Contains(res.Requests[1].System, "team vault") {
						t.Errorf("request 2 system missing the mid-turn Remember write:\n%s", res.Requests[1].System)
					}
				},
			},
		},
		{
			// Permission denial: in default mode with the headless DenyAsker, a
			// mutating call is refused before Execute and the file is untouched.
			sc: agenteval.Scenario{
				Name:   "permission_denied",
				Prompt: "Change world to korai in main.txt",
				Files:  map[string]string{"main.txt": "hello world\n"},
				Mode:   perm.ModeDefault, // Asker nil -> DenyAsker
				Setup:  readEditTools,
				Turns: []agenteval.Turn{
					{Calls: []agenteval.Call{{ID: "c1", Name: "Edit", Input: `{"path":"main.txt","old_string":"world","new_string":"korai"}`}}},
					{Text: "I was not permitted to edit the file."},
				},
			},
			want: expect{
				seq:   nil,
				files: map[string]string{"main.txt": "hello world\n"},
				metrics: agenteval.Metrics{
					ModelCalls: 2, ToolResults: 1, ToolErrors: 1, Denials: 1,
				},
				finalContains: "not permitted",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.sc.Name, func(t *testing.T) {
			t.Parallel()
			workDir := t.TempDir()

			res, err := agenteval.Run(context.Background(), tc.sc, workDir)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if res.Err != nil {
				t.Fatalf("engine error: %v", res.Err)
			}

			if diff := cmp.Diff(tc.want.metrics, res.Metrics); diff != "" {
				t.Errorf("metrics mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.seq, res.ToolSequence); diff != "" {
				t.Errorf("tool sequence mismatch (-want +got):\n%s", diff)
			}
			if tc.want.finalContains != "" && !strings.Contains(res.FinalText, tc.want.finalContains) {
				t.Errorf("final text %q missing %q", res.FinalText, tc.want.finalContains)
			}
			for rel, want := range tc.want.files {
				data, rerr := os.ReadFile(filepath.Join(workDir, rel))
				if rerr != nil {
					t.Errorf("reading %s: %v", rel, rerr)
					continue
				}
				if diff := cmp.Diff(want, string(data)); diff != "" {
					t.Errorf("final content of %s mismatch (-want +got):\n%s", rel, diff)
				}
			}
			if tc.want.extra != nil {
				tc.want.extra(t, res)
			}
			checkGolden(t, tc.sc.Name, res.Transcript)
		})
	}
}

// TestScriptExhaustionSurfaces verifies the harness fails loudly (not by
// hanging or panicking) when the engine needs more turns than were scripted.
func TestScriptExhaustionSurfaces(t *testing.T) {
	t.Parallel()

	sc := agenteval.Scenario{
		Name:   "exhausted",
		Prompt: "go",
		Mode:   perm.ModeBypassPermissions,
		Setup:  readEditTools,
		Turns: []agenteval.Turn{
			{Calls: []agenteval.Call{{ID: "c1", Name: "ReadFile", Input: `{"path":"missing.txt"}`}}},
			// No turn scripted for the model call after the tool result.
		},
	}
	res, err := agenteval.Run(context.Background(), sc, t.TempDir())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "script exhausted") {
		t.Errorf("Result.Err = %v, want a script-exhausted error", res.Err)
	}
}

// lastUser returns the text of a request's last user message.
func lastUser(req apiclient.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != apiclient.RoleUser {
			continue
		}
		var b strings.Builder
		for _, blk := range req.Messages[i].Content {
			if txt, ok := blk.(apiclient.TextBlock); ok {
				b.WriteString(txt.Text)
			}
		}
		return b.String()
	}
	return ""
}

// checkGolden compares transcript against the scenario's golden file,
// rewriting it under -update.
func checkGolden(t *testing.T, name, transcript string) {
	t.Helper()
	goldenPath := filepath.Join("..", "..", "testdata", "golden", "agenteval", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(transcript), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing — run with -update to create it: %v", err)
	}
	if diff := cmp.Diff(string(wantBytes), transcript); diff != "" {
		t.Errorf("transcript mismatch (-want +got):\n%s", diff)
	}
}
