// Package config holds the settings hierarchy resolved from defaults, config
// files, and flag overrides. Settings are resolved with the precedence
// defaults < user < project < local < flag overrides. All values are explicit
// and injected; there are no globals.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Settings is the resolved configuration for a session.
type Settings struct {
	Model          string                   `json:"model,omitempty"`
	PermissionMode string                   `json:"permissionMode,omitempty"`
	Permissions    Permissions              `json:"permissions,omitempty"`
	MCPServers     map[string]MCPServerSpec `json:"mcpServers,omitempty"`
	Hooks          map[string][]HookSpec    `json:"hooks,omitempty"`
	// LSP toggles language-server diagnostics (errors appended to edit/write
	// results). nil means enabled — it only starts a server when one is on PATH
	// for an edited file, so it is a no-op otherwise. Set false to disable.
	LSP *bool `json:"lsp,omitempty"`
}

// HookSpec is one shell command to run for a lifecycle event. Plain data; the
// command layer translates it into the hook package's type.
type HookSpec struct {
	Command string `json:"command"`
}

// Permissions holds the allow and deny rule lists applied to tool use.
type Permissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// MCPServerSpec describes how to launch a stdio MCP server. Plain data; the
// command layer translates it into a connection.
type MCPServerSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Defaults returns the baseline Settings used before any config file or flag
// override is applied: the "default" permission mode and empty permission and
// MCP-server collections. Model is intentionally empty so the active backend
// chooses its own default when neither config nor the --model flag sets one.
func Defaults() Settings {
	return Settings{
		Model:          "",
		PermissionMode: "default",
		Permissions:    Permissions{},
		MCPServers:     map[string]MCPServerSpec{},
	}
}

// Merge combines base and override into a single Settings, with override taking
// precedence. The merge semantics are:
//
//   - Scalar fields (Model, PermissionMode): override wins when its value is
//     non-empty; otherwise the base value is preserved.
//   - Permissions.Allow and Permissions.Deny: union-appended, base entries
//     first followed by override entries (no de-duplication).
//   - MCPServers: merged by key; on a key collision the override entry wins.
//     The result is always a non-nil map.
func Merge(base, override Settings) Settings {
	merged := base

	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.PermissionMode != "" {
		merged.PermissionMode = override.PermissionMode
	}

	merged.Permissions = Permissions{
		Allow: appendUnion(base.Permissions.Allow, override.Permissions.Allow),
		Deny:  appendUnion(base.Permissions.Deny, override.Permissions.Deny),
	}

	servers := make(map[string]MCPServerSpec, len(base.MCPServers)+len(override.MCPServers))
	for k, v := range base.MCPServers {
		servers[k] = v
	}
	for k, v := range override.MCPServers {
		servers[k] = v
	}
	merged.MCPServers = servers

	merged.Hooks = mergeHooks(base.Hooks, override.Hooks)

	return merged
}

// mergeHooks unions the hook specs per event, base entries before override.
// Returns nil when both inputs are empty so an absent config stays absent.
func mergeHooks(base, override map[string][]HookSpec) map[string][]HookSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string][]HookSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = append(out[k], v...)
	}
	for k, v := range override {
		out[k] = append(out[k], v...)
	}
	return out
}

// appendUnion returns base followed by override as a new slice. A nil result is
// returned only when both inputs are empty, so an absent list stays absent.
func appendUnion(base, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(override))
	out = append(out, base...)
	out = append(out, override...)
	return out
}

// Loader resolves the settings hierarchy from a fixed set of file paths. An
// empty path is treated as "no such file" and skipped.
type Loader struct {
	UserPath    string
	ProjectPath string
	LocalPath   string
}

// DefaultPaths builds a Loader using the conventional korai settings locations
// rooted at the given home and cwd directories. Home and cwd are injected by
// the caller (rather than resolved here) to keep the Loader hermetic and
// testable.
func DefaultPaths(home, cwd string) Loader {
	return Loader{
		UserPath:    filepath.Join(home, ".korai", "settings.json"),
		ProjectPath: filepath.Join(cwd, ".korai", "settings.json"),
		LocalPath:   filepath.Join(cwd, ".korai", "settings.local.json"),
	}
}

// Load resolves the settings hierarchy, starting from Defaults and merging the
// user, project, and local files in that order of increasing precedence. A
// missing file is skipped silently; a file that exists but cannot be parsed is
// an error wrapped with %w and the offending path.
func (l Loader) Load() (Settings, error) {
	resolved := Defaults()
	for _, path := range []string{l.UserPath, l.ProjectPath, l.LocalPath} {
		s, ok, err := readFile(path)
		if err != nil {
			return Settings{}, err
		}
		if !ok {
			continue
		}
		resolved = Merge(resolved, s)
	}
	return resolved, nil
}

// readFile reads and parses a single settings file. It reports ok=false (with a
// nil error) when the path is empty or the file does not exist, and wraps any
// parse failure with the path.
func readFile(path string) (Settings, bool, error) {
	if path == "" {
		return Settings{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Settings{}, false, nil
		}
		return Settings{}, false, fmt.Errorf("reading settings %s: %w", path, err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, false, fmt.Errorf("parsing settings %s: %w", path, err)
	}
	return s, true, nil
}
