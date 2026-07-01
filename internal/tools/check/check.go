// Package check implements the RunChecks tool: it runs the project's configured
// verification commands (lint, tests, build) through the OS shell and returns
// their combined output so the model can confirm a change did not break anything.
//
// Name mapping: the conceptual RunChecksTool becomes package check, constructor
// check.New, type check.Tool. The tool runs arbitrary configured commands, so it
// is fail-closed: ReadOnly and ConcurrencySafe are both false.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// maxOutputBytes caps the per-command output retained in the report so a noisy
// command cannot flood the model's context. The tail is kept (errors usually
// surface last).
const maxOutputBytes = 4 << 10

// Input is the structured input for the RunChecks tool. It takes no fields: the
// commands are configured at construction time, not chosen by the model.
type Input struct{}

// Tool implements tool.Tool for running the configured verification commands.
type Tool struct {
	commands []string
}

// New returns a RunChecks tool that runs the given commands, captured from
// settings.Checks at construction time.
func New(commands []string) *Tool {
	return &Tool{commands: commands}
}

// Name returns "RunChecks".
func (t *Tool) Name() string { return "RunChecks" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Runs the project's configured verification commands (lint/tests/build) " +
		"and returns their combined output; use it after making changes to confirm " +
		"nothing broke."
}

// InputSchema returns the JSON schema for RunChecks's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — checks may run arbitrary commands that mutate state
// (fail-closed).
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — checks must run serially (fail-closed).
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission gates RunChecks execution by permission mode, mirroring the
// Bash tool's policy:
//   - ModeBypassPermissions: allow without prompting.
//   - ModePlan: deny — no shell execution is permitted while planning.
//   - ModeDefault and ModeAcceptEdits: ask the user before running.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	switch mode {
	case perm.ModeBypassPermissions:
		return perm.DecisionAllow
	case perm.ModePlan:
		return perm.DecisionDeny
	case perm.ModeDefault, perm.ModeAcceptEdits:
		return perm.DecisionAsk
	default:
		// Fail-closed: unknown modes ask rather than silently proceed.
		return perm.DecisionAsk
	}
}

// Execute runs each configured check in deps.WorkDir via the OS shell, capturing
// combined stdout and stderr and the exit code, and assembles a report. When no
// checks are configured it returns a friendly (non-error) result. Result.IsError
// is set when any check fails, but the full report is always included so the
// model can read and fix the failures. Context cancellation stops further checks
// and is honored by each running command.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return tool.Result{}, fmt.Errorf("runchecks: invalid input: %w", err)
		}
	}

	if len(t.commands) == 0 {
		return tool.Result{
			Content: "no checks are configured; set \"checks\" in your korai settings " +
				"to a list of verification commands (e.g. lint, tests, build).",
		}, nil
	}

	var report strings.Builder
	anyFailed := false

	for _, command := range t.commands {
		fmt.Fprintf(&report, "$ %s\n", command)

		output, exitCode, runErr := t.run(ctx, command, deps.WorkDir)

		switch {
		case runErr != nil:
			anyFailed = true
			fmt.Fprintf(&report, "FAIL (%v)\n", runErr)
		case exitCode != 0:
			anyFailed = true
			fmt.Fprintf(&report, "FAIL (exit %d)\n", exitCode)
		default:
			report.WriteString("PASS\n")
		}

		if trimmed := truncate(output); trimmed != "" {
			report.WriteString(trimmed)
			if !strings.HasSuffix(trimmed, "\n") {
				report.WriteByte('\n')
			}
		}
		report.WriteByte('\n')
	}

	return tool.Result{
		Content: strings.TrimRight(report.String(), "\n"),
		IsError: anyFailed,
	}, nil
}

// run executes a single command through the OS shell in dir, returning its
// combined output and exit code. A non-zero exit is reported via exitCode (not
// err); err is non-nil only when the command could not be launched or run to
// completion (e.g. shell missing, context cancellation).
func (t *Tool) run(ctx context.Context, command, dir string) (output string, exitCode int, err error) {
	name, args := shell(command)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	out, runErr := cmd.CombinedOutput()
	output = string(out)
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return output, exitErr.ExitCode(), nil
		}
		return output, -1, runErr
	}
	return output, 0, nil
}

// shell returns the OS shell program and arguments that run command as a single
// shell line. On Windows it uses cmd /C; everywhere else sh -c. This mirrors the
// Bash tool's cross-platform shell selection.
func shell(command string) (name string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}

// truncate keeps the last maxOutputBytes of s, prefixing a marker when content
// was dropped so the model knows the output is partial.
func truncate(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return "... (output truncated) ...\n" + s[len(s)-maxOutputBytes:]
}
