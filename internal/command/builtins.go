package command

import (
	"fmt"
	"strings"
)

// RegisterBuiltins adds the standard local commands: help, clear, and quit.
// toolNames supplies the registered tool names for the /tools listing; pass nil
// to omit it.
func RegisterBuiltins(r *Registry, toolNames func() []string) {
	r.Register(&helpCommand{registry: r})
	r.Register(&clearCommand{})
	r.Register(&quitCommand{})
	if toolNames != nil {
		r.Register(&toolsCommand{names: toolNames})
	}
}

// helpCommand lists the available commands.
type helpCommand struct{ registry *Registry }

func (*helpCommand) Name() string        { return "help" }
func (*helpCommand) Description() string { return "list available commands" }
func (c *helpCommand) Run(string) (Result, error) {
	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, cmd := range c.registry.All() {
		fmt.Fprintf(&b, "  /%-12s %s\n", cmd.Name(), cmd.Description())
	}
	return Result{Action: ShowText, Text: strings.TrimRight(b.String(), "\n")}, nil
}

// clearCommand clears the transcript.
type clearCommand struct{}

func (*clearCommand) Name() string        { return "clear" }
func (*clearCommand) Description() string { return "clear the conversation transcript" }
func (*clearCommand) Run(string) (Result, error) {
	return Result{Action: Clear}, nil
}

// quitCommand exits the program.
type quitCommand struct{}

func (*quitCommand) Name() string        { return "quit" }
func (*quitCommand) Description() string { return "exit Korai" }
func (*quitCommand) Run(string) (Result, error) {
	return Result{Action: Quit}, nil
}

// toolsCommand lists the registered tools.
type toolsCommand struct{ names func() []string }

func (*toolsCommand) Name() string        { return "tools" }
func (*toolsCommand) Description() string { return "list available tools" }
func (c *toolsCommand) Run(string) (Result, error) {
	names := c.names()
	if len(names) == 0 {
		return Result{Action: ShowText, Text: "No tools registered."}, nil
	}
	return Result{Action: ShowText, Text: "Tools:\n  " + strings.Join(names, "\n  ")}, nil
}
