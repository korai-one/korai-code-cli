// Package todo implements the TodoWrite tool: the agent's task list for
// multi-step work. Each call replaces the whole list with the items supplied by
// the model and returns the rendered list so the model always sees current state.
//
// Conceptual mapping: the reference CLI's task-list write action becomes package
// todo (under internal/tools) exporting tool.Tool via todo.New. The list is held
// in a caller-supplied internal/todo.List; the tool mutates only in-session state
// and never touches files or the network.
package todo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/todo"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Input is the structured input for the TodoWrite tool. The model passes the
// complete list on every call; the tool replaces the existing list with it.
type Input struct {
	// Todos is the full task list to store, replacing any previous list.
	Todos []TodoInput `json:"todos" jsonschema:"required,description=The complete task list, replacing the previous list. Pass every task on each call."`
}

// TodoInput is one task in the TodoWrite input.
type TodoInput struct {
	// Content is the task description in imperative form (e.g. "Run tests").
	Content string `json:"content" jsonschema:"required,description=The task description"`
	// Status is one of pending, in_progress, or completed.
	Status string `json:"status" jsonschema:"required,description=Task status: one of pending, in_progress, completed"`
	// ActiveForm is the present-continuous label shown while in progress
	// (e.g. "Running tests").
	ActiveForm string `json:"active_form,omitempty" jsonschema:"description=Present-continuous label shown while the task is in progress, e.g. Running tests"`
}

// Tool implements tool.Tool for updating the session todo list.
type Tool struct {
	list *todo.List
}

// New returns a new TodoWrite tool backed by the given session todo list.
func New(list *todo.List) *Tool {
	return &Tool{list: list}
}

// Name returns "TodoWrite".
func (t *Tool) Name() string { return "TodoWrite" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Tracks a task list for multi-step work. Pass the full list on every " +
		"call; this replaces the previous list. Each task has a status of " +
		"pending, in_progress, or completed. Keep exactly one task in_progress " +
		"at a time, and mark a task completed as soon as it is done."
}

// InputSchema returns the JSON schema for TodoWrite's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — TodoWrite replaces the session todo list.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — list updates are applied serially.
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission always allows TodoWrite regardless of permission mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	// TodoWrite only mutates in-session state, never files or the network, so it
	// is safe to allow without prompting in every permission mode.
	_ = mode
	return perm.DecisionAllow
}

// Execute replaces the session todo list with Input.Todos and returns the
// rendered list. Invalid input JSON is a hard error; an invalid status is a soft
// error returned as a Result with IsError set. It honors ctx cancellation and
// never prints.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("todo: invalid input: %w", err)
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	items := make([]todo.Item, len(in.Todos))
	for i, td := range in.Todos {
		status, ok := parseStatus(td.Status)
		if !ok {
			return tool.Result{
				Content: fmt.Sprintf("invalid status %q for todo %q: want pending, in_progress, or completed", td.Status, td.Content),
				IsError: true,
			}, nil
		}
		items[i] = todo.Item{
			Content:    td.Content,
			Status:     status,
			ActiveForm: td.ActiveForm,
		}
	}

	t.list.Set(items)
	return tool.Result{Content: t.list.Render()}, nil
}

// parseStatus validates a status string and maps it to a todo.Status. The second
// return value reports whether the input was one of the three valid statuses.
func parseStatus(s string) (todo.Status, bool) {
	switch todo.Status(s) {
	case todo.StatusPending, todo.StatusInProgress, todo.StatusCompleted:
		return todo.Status(s), true
	default:
		return "", false
	}
}
