// Package command defines slash commands and their registry. A command is
// parsed from input like "/name args" and produces a Result telling the host
// (the TUI) what to do. Commands are host-agnostic: they return data, the host
// acts on it — the same separation the engine keeps from the UI.
package command

import (
	"sort"
	"strings"
)

// Action tells the host what to do after a command runs.
type Action int

const (
	// ShowText displays Result.Text to the user without involving the model.
	ShowText Action = iota
	// Clear resets the transcript.
	Clear
	// Quit exits the program.
	Quit
	// SubmitPrompt sends Result.Text to the model as a user message.
	SubmitPrompt
)

// Result is what a command returns to the host.
type Result struct {
	Action Action
	Text   string
}

// Command is a single slash command.
type Command interface {
	// Name is the command word without the leading slash (e.g. "help").
	Name() string
	// Description is a one-line summary shown by /help.
	Description() string
	// Run executes the command with the raw argument string (may be empty).
	Run(args string) (Result, error)
}

// Parse splits raw input into a command name and argument string. It reports
// isCommand=false when the input is not a slash command.
func Parse(input string) (name, args string, isCommand bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	body := strings.TrimPrefix(trimmed, "/")
	name, args, _ = strings.Cut(body, " ")
	return name, strings.TrimSpace(args), name != ""
}

// Registry holds the available commands.
type Registry struct {
	cmds map[string]Command
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{cmds: make(map[string]Command)}
}

// Register adds c, replacing any command with the same name (so project skills
// can override built-ins of the same name).
func (r *Registry) Register(c Command) {
	r.cmds[c.Name()] = c
}

// Get returns the command with the given name.
func (r *Registry) Get(name string) (Command, bool) {
	c, ok := r.cmds[name]
	return c, ok
}

// All returns every command sorted by name.
func (r *Registry) All() []Command {
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
