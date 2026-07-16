package command

import (
	"fmt"
	"strings"
)

// WorkerSelector is the worker-mode state a /worker_mode command reads and
// writes. *apiclient.ClientSelector satisfies it; the interface keeps this
// package decoupled from apiclient (the same pattern as Selector for /model).
type WorkerSelector interface {
	// Mode returns the active mode ("local" or "remote").
	Mode() string
	// SetMode switches the active backend, or returns an error explaining why it
	// could not (unknown mode, or that backend not configured this session).
	SetMode(mode string) error
	// Available lists the modes that have a configured backend.
	Available() []string
}

// workerCommand shows or switches the inference backend locality.
type workerCommand struct {
	selector WorkerSelector
}

// NewWorkerCommand returns a /worker_mode command. With no argument it shows the
// active mode and the available ones; with an argument (local | remote) it
// switches inference to that backend for subsequent turns.
func NewWorkerCommand(selector WorkerSelector) Command {
	return &workerCommand{selector: selector}
}

// Name returns "worker_mode".
func (*workerCommand) Name() string { return "worker_mode" }

// Description returns the command summary.
func (*workerCommand) Description() string {
	return "show or switch the inference backend (local | remote)"
}

// Run shows the status when args is empty, otherwise switches to args.
func (c *workerCommand) Run(args string) (Result, error) {
	target := strings.TrimSpace(args)
	if target == "" {
		return Result{Action: ShowText, Text: c.status()}, nil
	}
	if err := c.selector.SetMode(target); err != nil {
		return Result{Action: ShowText, Text: err.Error() + "\n\n" + c.status()}, nil
	}
	return Result{Action: ShowText, Text: "switched inference to " + target}, nil
}

// status renders the active mode alongside the available modes, marking the
// current one.
func (c *workerCommand) status() string {
	current := c.selector.Mode()
	var b strings.Builder
	b.WriteString("Inference backend:")
	for _, m := range c.selector.Available() {
		marker := "  "
		if m == current {
			marker = "* "
		}
		fmt.Fprintf(&b, "\n  %s%s", marker, m)
	}
	b.WriteString("\n\nUse /worker_mode local or /worker_mode remote to switch.")
	return b.String()
}
