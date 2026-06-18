package command

import (
	"fmt"
	"sort"
	"strings"
)

// Selector is the model state a /model command reads and writes. *apiclient.
// ModelSelector satisfies it; the interface keeps this package decoupled from
// apiclient.
type Selector interface {
	Get() string
	Set(model string)
}

// modelCommand lists a fixed set of models and switches between them.
type modelCommand struct {
	available []string
	selector  Selector
}

// NewModelCommand returns a /model command. With no argument it lists the
// available models (marking the current one); with an argument it switches to
// that model if it is in the available set.
func NewModelCommand(available []string, selector Selector) Command {
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)
	return &modelCommand{available: sorted, selector: selector}
}

// Name returns "model".
func (*modelCommand) Name() string { return "model" }

// Description returns the command summary.
func (*modelCommand) Description() string { return "show or switch the active model" }

// Run lists models when args is empty, otherwise switches to args.
func (c *modelCommand) Run(args string) (Result, error) {
	current := c.selector.Get()
	target := strings.TrimSpace(args)

	if target == "" {
		return Result{Action: ShowText, Text: c.list(current)}, nil
	}
	if !c.isAvailable(target) {
		return Result{
			Action: ShowText,
			Text:   fmt.Sprintf("unknown model %q\n%s", target, c.list(current)),
		}, nil
	}
	c.selector.Set(target)
	return Result{Action: ShowText, Text: "switched model to " + target}, nil
}

func (c *modelCommand) isAvailable(model string) bool {
	for _, m := range c.available {
		if m == model {
			return true
		}
	}
	return false
}

// list renders the available models, marking the current one.
func (c *modelCommand) list(current string) string {
	var b strings.Builder
	b.WriteString("Available models:")
	for _, m := range c.available {
		marker := "  "
		if m == current {
			marker = "* "
		}
		fmt.Fprintf(&b, "\n  %s%s", marker, m)
	}
	if !c.isAvailable(current) {
		fmt.Fprintf(&b, "\n  * %s (current)", current)
	}
	return b.String()
}
