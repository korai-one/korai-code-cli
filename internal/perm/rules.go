package perm

// Rules are explicit allow/deny policies sourced from settings, CLI flags, or
// the session. They are matched by tool name; "*" matches any tool. Deny always
// takes precedence over allow (fail-closed).
//
// Argument-level matching (e.g. allowing only specific Bash commands) is a
// deliberate future extension; this first pass matches on tool name alone.
type Rules struct {
	// Allow lists tool-name patterns that upgrade an "ask" decision to allowed.
	Allow []string
	// Deny lists tool-name patterns that force a denial regardless of the
	// tool's own decision.
	Deny []string
}

// AllowsTool reports whether name matches any allow pattern.
func (r Rules) AllowsTool(name string) bool {
	return matchAny(r.Allow, name)
}

// DeniesTool reports whether name matches any deny pattern.
func (r Rules) DeniesTool(name string) bool {
	return matchAny(r.Deny, name)
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if p == "*" || p == name {
			return true
		}
	}
	return false
}
