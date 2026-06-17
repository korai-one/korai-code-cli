// Package config holds the settings hierarchy resolved from flags, environment,
// and config files. All values are explicit and injected; there are no globals.
package config

// Config holds resolved configuration for a korai session.
type Config struct {
	// APIKey is the inference backend API key (from ANTHROPIC_API_KEY or flag).
	APIKey string
	// Model is the model identifier to use for inference.
	Model string
	// WorkDir is the working directory for tool execution.
	WorkDir string
	// SystemPrompt is prepended to every conversation as the system context.
	SystemPrompt string
}
