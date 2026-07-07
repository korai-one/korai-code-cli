package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// resolveTimeout bounds each command substitution spawned by ResolveValue so a
// hung command cannot block config loading indefinitely.
const resolveTimeout = 30 * time.Second

// ResolveValue expands shell-style references inside a single config string
// value and returns the result. Three token forms are recognized and resolved
// in a single left-to-right pass:
//
//   - ${VAR} and $VAR expand to os.Getenv("VAR"); an unset variable becomes the
//     empty string, matching shell behavior.
//   - $(command) runs command via "bash -c", captures stdout, trims trailing
//     newlines, and substitutes the result. Each command is bounded by a 30s
//     timeout derived from ctx, and ctx cancellation is honored.
//
// Because resolution is a single pass, the text produced by a command
// substitution is inserted literally and never re-scanned: a $VAR or $(...) that
// appears in a command's output is left untouched. A string containing none of
// these tokens (no '$') is returned unchanged without spawning any subprocess. A
// command that fails to start or exits non-zero is reported as a wrapped error.
func ResolveValue(ctx context.Context, s string) (string, error) {
	// Fast path: nothing to expand, so no subprocess is ever spawned.
	if !strings.Contains(s, "$") {
		return s, nil
	}

	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}

		// A trailing '$' is a literal dollar sign.
		if i+1 >= len(s) {
			b.WriteByte('$')
			i++
			continue
		}

		switch next := s[i+1]; {
		case next == '(':
			end, ok := matchParen(s, i+1)
			if !ok {
				// Unbalanced parentheses: treat the '$' as a literal and move on.
				b.WriteByte('$')
				i++
				continue
			}
			command := s[i+2 : end]
			out, err := runCommand(ctx, command)
			if err != nil {
				return "", fmt.Errorf("resolving command substitution %q: %w", command, err)
			}
			b.WriteString(out)
			i = end + 1
		case next == '{':
			rel := strings.IndexByte(s[i+1:], '}')
			if rel < 0 {
				// Unbalanced braces: treat the '$' as a literal and move on.
				b.WriteByte('$')
				i++
				continue
			}
			name := s[i+2 : i+1+rel]
			b.WriteString(os.Getenv(name))
			i = i + 1 + rel + 1
		case isNameByte(next):
			j := i + 1
			for j < len(s) && isNameByte(s[j]) {
				j++
			}
			b.WriteString(os.Getenv(s[i+1 : j]))
			i = j
		default:
			// '$' followed by anything else (space, symbol) is a literal.
			b.WriteByte('$')
			i++
		}
	}

	return b.String(), nil
}

// isNameByte reports whether c may appear in an environment-variable name for
// the purposes of $VAR expansion: an ASCII letter, digit, or underscore.
func isNameByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// matchParen returns the index of the ')' that closes the '(' at position open
// (s[open] must be '('), accounting for nested parentheses. ok is false when no
// matching ')' exists.
func matchParen(s string, open int) (idx int, ok bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// runCommand runs a single command via "bash -c" and returns its stdout with
// trailing newlines trimmed. The invocation is bounded by resolveTimeout, derived
// from ctx so caller cancellation still applies. A non-zero exit or a failure to
// launch is returned as a wrapped error carrying any stderr text.
func runCommand(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

// resolveSettings returns a copy of s with shell-style references expanded in the
// value fields where substitution is meaningful and safe: Model and every
// MCPServers[*].Env value. Fields that are themselves shell commands executed
// later — Checks, Hooks[*].Command, and MCPServers[*].Command / Args — are left
// untouched so the downstream shell (not this pass) performs any $(...) expansion,
// avoiding surprising double execution. The originals are not mutated. The first
// field that fails to resolve returns a wrapped error naming it.
func resolveSettings(ctx context.Context, s Settings) (Settings, error) {
	out := s

	model, err := ResolveValue(ctx, s.Model)
	if err != nil {
		return Settings{}, fmt.Errorf("resolving model: %w", err)
	}
	out.Model = model

	if len(s.MCPServers) > 0 {
		servers := make(map[string]MCPServerSpec, len(s.MCPServers))
		for name, spec := range s.MCPServers {
			if len(spec.Env) > 0 {
				env := make(map[string]string, len(spec.Env))
				for k, v := range spec.Env {
					rv, err := ResolveValue(ctx, v)
					if err != nil {
						return Settings{}, fmt.Errorf("resolving mcpServers.%s.env.%s: %w", name, k, err)
					}
					env[k] = rv
				}
				spec.Env = env
			}
			servers[name] = spec
		}
		out.MCPServers = servers
	}

	return out, nil
}
