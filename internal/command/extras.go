package command

import "strings"

// costCommand reports cumulative token usage and estimated cost.
type costCommand struct{ report func() string }

// NewCostCommand returns a /cost command that shows the result of report.
func NewCostCommand(report func() string) Command {
	return &costCommand{report: report}
}

// Name returns "cost".
func (*costCommand) Name() string { return "cost" }

// Description returns the command summary.
func (*costCommand) Description() string { return "show token usage and estimated cost" }

// Run renders the usage report.
func (c *costCommand) Run(string) (Result, error) {
	return Result{Action: ShowText, Text: c.report()}, nil
}

// compactCommand asks the host to compact the conversation history.
type compactCommand struct{}

// NewCompactCommand returns a /compact command. The host performs the actual
// summarization when it sees the CompactHistory action.
func NewCompactCommand() Command { return &compactCommand{} }

// Name returns "compact".
func (*compactCommand) Name() string { return "compact" }

// Description returns the command summary.
func (*compactCommand) Description() string {
	return "summarize earlier turns to free up context"
}

// Run signals the host to compact the transcript.
func (*compactCommand) Run(string) (Result, error) {
	return Result{Action: CompactHistory}, nil
}

// resumeCommand lists saved sessions, or asks the host to load one by id.
type resumeCommand struct{ list func() string }

// NewResumeCommand returns a /resume command. With no argument it shows the
// saved-session list from list; with an id it asks the host to load that session.
func NewResumeCommand(list func() string) Command {
	return &resumeCommand{list: list}
}

// Name returns "resume".
func (*resumeCommand) Name() string { return "resume" }

// Description returns the command summary.
func (*resumeCommand) Description() string {
	return "list saved sessions, or /resume <id> to load one"
}

// Run lists sessions when args is empty, otherwise signals a load.
func (c *resumeCommand) Run(args string) (Result, error) {
	id := strings.TrimSpace(args)
	if id == "" {
		return Result{Action: ShowText, Text: c.list()}, nil
	}
	return Result{Action: ResumeSession, Text: id}, nil
}

// aboutCommand shows version and project information.
type aboutCommand struct{ text string }

// NewAboutCommand returns an /about command that displays text — typically the
// version and a one-line description of the project.
func NewAboutCommand(text string) Command { return &aboutCommand{text: text} }

// Name returns "about".
func (*aboutCommand) Name() string { return "about" }

// Description returns the command summary.
func (*aboutCommand) Description() string { return "show version and project information" }

// Run displays the about text.
func (c *aboutCommand) Run(string) (Result, error) {
	return Result{Action: ShowText, Text: c.text}, nil
}

// planCommand toggles plan mode, or with an argument enters plan mode and plans
// the given task immediately.
type planCommand struct {
	toggle    func() string
	enterPlan func()
}

// NewPlanCommand returns a /plan command. toggle switches plan mode on/off and
// returns the resulting mode name; enterPlan unconditionally enters plan mode,
// used when a task is supplied so it is planned right away.
func NewPlanCommand(toggle func() string, enterPlan func()) Command {
	return &planCommand{toggle: toggle, enterPlan: enterPlan}
}

// Name returns "plan".
func (*planCommand) Name() string { return "plan" }

// Description returns the command summary.
func (*planCommand) Description() string {
	return "/plan <task> to plan a task, or /plan alone to toggle plan mode"
}

// Run enters plan mode and submits the task when one is given; otherwise it
// toggles plan mode and reports the new mode.
func (c *planCommand) Run(args string) (Result, error) {
	if task := strings.TrimSpace(args); task != "" {
		c.enterPlan()
		return Result{Action: SubmitPrompt, Text: task}, nil
	}
	return Result{Action: ShowText, Text: "permission mode: " + c.toggle()}, nil
}
