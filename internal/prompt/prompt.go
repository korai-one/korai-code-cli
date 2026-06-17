// Package prompt holds the agent's system prompt and composes it with the
// per-session environment context.
package prompt

import "strings"

// agentSystem is the base identity and operating instructions given to the
// model on every turn. It is intentionally terse; environment context (working
// directory, git status, project instructions) is appended by Compose.
const agentSystem = `You are Korai, an AI coding agent that runs in a terminal.

You help with software engineering tasks: reading and understanding code,
explaining how things work, and making focused changes.

Guidelines:
- Use the available tools to inspect files before answering questions about them.
  Do not guess at file contents you can read.
- Keep responses concise and direct. Avoid preamble and filler.
- When you have gathered enough information to answer, stop calling tools and
  give the answer.
- Reference files as path:line so they are easy to locate.`

// Compose returns the full system prompt: the agent instructions followed by
// the session's environment context. envContext may be empty.
func Compose(envContext string) string {
	if envContext == "" {
		return agentSystem
	}
	var b strings.Builder
	b.WriteString(agentSystem)
	b.WriteString("\n\n# Environment\n\n")
	b.WriteString(envContext)
	return b.String()
}
