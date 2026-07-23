package agenteval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
	"github.com/Nevaero/korai-code-cli/internal/tools/write"
)

// TestRunLiveHarness exercises the live harness hermetically by substituting a
// scripted client for the real backend: records, seeds, JSONL output, and the
// pass/fail scoring must all behave as they would against a live endpoint.
func TestRunLiveHarness(t *testing.T) {
	t.Parallel()

	scenarios := []LiveScenario{
		{
			Name:   "scripted_write",
			Prompt: "write it",
			Tools:  func() []tool.Tool { return []tool.Tool{write.New(), readfile.New()} },
			Check: func(workDir, _ string) error {
				return checkFileTrimmed(workDir, "out.txt", "payload")
			},
		},
		{
			Name:   "scripted_fail",
			Prompt: "echo the codeword",
			Tools:  func() []tool.Tool { return []tool.Tool{readfile.New()} },
			Check: func(_, finalText string) error {
				if !strings.Contains(finalText, "MISSING-WORD") {
					return errNoCodeword
				}
				return nil
			},
		},
	}

	// The scripted "model" writes out.txt on its first call and answers in
	// prose on every later call — enough for one scenario to pass and the
	// other to fail its check.
	client := &scriptClient{turns: []Turn{
		{Calls: []Call{{ID: "c1", Name: "Write", Input: `{"path":"out.txt","content":"payload\n"}`}}},
		{Text: "done"},
		{Text: "no codeword here"},
	}}

	var jsonl bytes.Buffer
	records, err := RunLive(context.Background(), LiveOptions{
		Client:    client,
		Scenarios: scenarios,
		Seed:      42,
		Timeout:   time.Minute,
		Records:   &jsonl,
	})
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}

	if !records[0].Pass || records[0].Scenario != "scripted_write" {
		t.Errorf("record 0 = %+v, want a pass for scripted_write", records[0])
	}
	if records[0].ToolCalls != 1 || records[0].ModelCalls != 2 {
		t.Errorf("record 0 metrics = %+v, want 1 tool call over 2 model calls", records[0])
	}
	if records[0].Seed != 42 {
		t.Errorf("record 0 seed = %d, want 42", records[0].Seed)
	}
	if records[1].Pass || records[1].Fail == "" {
		t.Errorf("record 1 = %+v, want a check failure", records[1])
	}

	// JSONL: one valid object per line, round-tripping to the same records.
	lines := strings.Split(strings.TrimSpace(jsonl.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSONL lines = %d, want 2", len(lines))
	}
	for i, line := range lines {
		var got LiveRecord
		if uerr := json.Unmarshal([]byte(line), &got); uerr != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, uerr)
		}
		if diff := cmp.Diff(records[i], got); diff != "" {
			t.Errorf("JSONL record %d mismatch (-want +got):\n%s", i, diff)
		}
	}
}

// errNoCodeword is the deterministic check failure used by the harness test.
var errNoCodeword = errors.New("final text does not echo the codeword")

// TestRunLiveRequiresClient verifies the misconfiguration guard.
func TestRunLiveRequiresClient(t *testing.T) {
	t.Parallel()
	if _, err := RunLive(context.Background(), LiveOptions{}); err == nil {
		t.Error("RunLive without a client must error")
	}
}

// TestFormatLiveSummary pins the summary table's shape and arithmetic.
func TestFormatLiveSummary(t *testing.T) {
	t.Parallel()

	records := []LiveRecord{
		{Scenario: "create_file", Run: 0, Pass: true, ModelCalls: 3},
		{Scenario: "create_file", Run: 1, Pass: false, Fail: "mismatch", ModelCalls: 5, FenceRetries: 1},
		{Scenario: "read_codeword", Run: 0, Pass: true, ModelCalls: 2},
	}
	got := FormatLiveSummary(records)

	for _, want := range []string{
		"korai eval summary",
		"create_file",
		"read_codeword",
		"50.0%",  // create_file pass rate
		"100.0%", // read_codeword pass rate
		"fence compliance: 90.0% (1 retried of 10 model calls)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
}
