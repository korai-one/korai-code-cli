package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/config"
)

func TestDefaults(t *testing.T) {
	want := config.Settings{
		Model:          "",
		PermissionMode: "default",
		Permissions:    config.Permissions{},
		MCPServers:     map[string]config.MCPServerSpec{},
	}
	if diff := cmp.Diff(want, config.Defaults()); diff != "" {
		t.Errorf("Defaults() mismatch (-want +got):\n%s", diff)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		base     config.Settings
		override config.Settings
		want     config.Settings
	}{
		{
			name:     "scalar override wins",
			base:     config.Settings{Model: "base-model", PermissionMode: "default"},
			override: config.Settings{Model: "override-model", PermissionMode: "plan"},
			want:     config.Settings{Model: "override-model", PermissionMode: "plan"},
		},
		{
			name:     "empty override preserves base scalars",
			base:     config.Settings{Model: "base-model", PermissionMode: "default"},
			override: config.Settings{},
			want:     config.Settings{Model: "base-model", PermissionMode: "default"},
		},
		{
			name: "allow and deny union-append base then override",
			base: config.Settings{
				Permissions: config.Permissions{Allow: []string{"a1"}, Deny: []string{"d1"}},
			},
			override: config.Settings{
				Permissions: config.Permissions{Allow: []string{"a2"}, Deny: []string{"d2", "d3"}},
			},
			want: config.Settings{
				Permissions: config.Permissions{
					Allow: []string{"a1", "a2"},
					Deny:  []string{"d1", "d2", "d3"},
				},
			},
		},
		{
			name: "mcp servers merge by key, override key wins",
			base: config.Settings{
				MCPServers: map[string]config.MCPServerSpec{
					"keep":      {Command: "keep-cmd"},
					"overwrite": {Command: "base-cmd", Args: []string{"old"}},
				},
			},
			override: config.Settings{
				MCPServers: map[string]config.MCPServerSpec{
					"overwrite": {Command: "new-cmd", Args: []string{"new"}},
					"added":     {Command: "added-cmd"},
				},
			},
			want: config.Settings{
				MCPServers: map[string]config.MCPServerSpec{
					"keep":      {Command: "keep-cmd"},
					"overwrite": {Command: "new-cmd", Args: []string{"new"}},
					"added":     {Command: "added-cmd"},
				},
			},
		},
		{
			name:     "lsp override (non-nil) wins",
			base:     config.Settings{LSP: boolPtr(true)},
			override: config.Settings{LSP: boolPtr(false)},
			want:     config.Settings{LSP: boolPtr(false)},
		},
		{
			name:     "lsp nil override preserves base",
			base:     config.Settings{LSP: boolPtr(false)},
			override: config.Settings{},
			want:     config.Settings{LSP: boolPtr(false)},
		},
		{
			name:     "checks non-empty override replaces base",
			base:     config.Settings{Checks: []string{"go build ./..."}},
			override: config.Settings{Checks: []string{"make check"}},
			want:     config.Settings{Checks: []string{"make check"}},
		},
		{
			name:     "checks empty override preserves base",
			base:     config.Settings{Checks: []string{"go vet ./..."}},
			override: config.Settings{},
			want:     config.Settings{Checks: []string{"go vet ./..."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.Merge(tt.base, tt.override)
			// Normalize the always-non-nil map for cases that do not exercise it.
			if tt.want.MCPServers == nil {
				tt.want.MCPServers = map[string]config.MCPServerSpec{}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Merge() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadNoFiles(t *testing.T) {
	dir := t.TempDir()
	l := config.Loader{
		UserPath:    filepath.Join(dir, "user.json"),
		ProjectPath: filepath.Join(dir, "project.json"),
		LocalPath:   filepath.Join(dir, "local.json"),
	}
	got, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if diff := cmp.Diff(config.Defaults(), got); diff != "" {
		t.Errorf("Load() with no files mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.json")
	projectPath := filepath.Join(dir, "project.json")
	localPath := filepath.Join(dir, "local.json")

	writeJSON(t, userPath, `{
		"model": "user-model",
		"permissionMode": "user-mode",
		"permissions": {"allow": ["user-allow"], "deny": ["user-deny"]},
		"mcpServers": {"shared": {"command": "user-cmd"}, "userOnly": {"command": "u"}}
	}`)
	writeJSON(t, projectPath, `{
		"model": "project-model",
		"permissions": {"allow": ["project-allow"]},
		"mcpServers": {"shared": {"command": "project-cmd"}, "projectOnly": {"command": "p"}}
	}`)
	writeJSON(t, localPath, `{
		"permissionMode": "local-mode",
		"permissions": {"deny": ["local-deny"]}
	}`)

	l := config.Loader{UserPath: userPath, ProjectPath: projectPath, LocalPath: localPath}
	got, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Settings{
		Model:          "project-model", // project overrides user; local leaves it
		PermissionMode: "local-mode",    // local overrides user; project leaves it
		Permissions: config.Permissions{
			Allow: []string{"user-allow", "project-allow"},
			Deny:  []string{"user-deny", "local-deny"},
		},
		MCPServers: map[string]config.MCPServerSpec{
			"shared":      {Command: "project-cmd"}, // project key wins over user
			"userOnly":    {Command: "u"},
			"projectOnly": {Command: "p"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Load() precedence mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadMissingFilesSkipped(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "project.json")
	writeJSON(t, projectPath, `{"model": "project-model"}`)

	l := config.Loader{
		UserPath:    filepath.Join(dir, "absent-user.json"),
		ProjectPath: projectPath,
		LocalPath:   filepath.Join(dir, "absent-local.json"),
	}
	got, err := l.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Model != "project-model" {
		t.Errorf("Model = %q, want %q", got.Model, "project-model")
	}
	if got.PermissionMode != "default" {
		t.Errorf("PermissionMode = %q, want default from Defaults()", got.PermissionMode)
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "project.json")
	writeJSON(t, badPath, `{"model": "x"`) // truncated

	l := config.Loader{ProjectPath: badPath}
	_, err := l.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for malformed JSON")
	}
	if !strings.Contains(err.Error(), badPath) {
		t.Errorf("error %q does not mention path %q", err.Error(), badPath)
	}
}

func TestDefaultPaths(t *testing.T) {
	got := config.DefaultPaths("/home/alice", "/work/proj")
	want := config.Loader{
		UserPath:    filepath.Join("/home/alice", ".korai", "settings.json"),
		ProjectPath: filepath.Join("/work/proj", ".korai", "settings.json"),
		LocalPath:   filepath.Join("/work/proj", ".korai", "settings.local.json"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DefaultPaths() mismatch (-want +got):\n%s", diff)
	}
}

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
