package command

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

// planCommand toggles plan mode (read-only research) on or off.
type planCommand struct{ toggle func() string }

// NewPlanCommand returns a /plan command. toggle switches plan mode on/off and
// returns the resulting mode name, which the command reports to the user.
func NewPlanCommand(toggle func() string) Command {
	return &planCommand{toggle: toggle}
}

// Name returns "plan".
func (*planCommand) Name() string { return "plan" }

// Description returns the command summary.
func (*planCommand) Description() string {
	return "toggle plan mode (read-only; agent proposes before acting)"
}

// Run toggles plan mode and reports the new mode.
func (c *planCommand) Run(string) (Result, error) {
	return Result{Action: ShowText, Text: "permission mode: " + c.toggle()}, nil
}
