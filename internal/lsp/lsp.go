// Package lsp gives the engine language-server diagnostics. It wraps
// charmbracelet/x/powernap: powernap's registry lazily starts the right
// language server for a file (gopls, tsserver, …) from a built-in catalog, and
// this Manager layers on top the pieces an agent needs — open-document version
// tracking, ingestion of publishDiagnostics into a versioned store, and a
// "settling" wait so a tool can report fresh diagnostics right after an edit.
//
// The model never sees this package; tools call Notify+WaitForDiagnostics after
// a write and append Report(...) (see diagnostic.go) to their result, so the
// model receives its own compile/type errors and fixes them in the same turn.
package lsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/config"
	plsp "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/charmbracelet/x/powernap/pkg/registry"

	"github.com/Nevaero/korai-code-cli/internal/csync"
)

const publishDiagnosticsMethod = "textDocument/publishDiagnostics"

// Settling timings: after a change we wait up to firstChangeWait for the server
// to emit anything, then consider it done once the diagnostics version has held
// steady for settleQuiet. Bounded overall by the caller's timeout.
const (
	firstChangeWait = time.Second
	settleQuiet     = 300 * time.Millisecond
	settlePoll      = 50 * time.Millisecond
)

// Manager owns the language-server registry and the diagnostics store. It is
// safe for concurrent use. A disabled Manager (enabled=false) is a no-op, so
// callers need not branch on whether LSP is configured.
type Manager struct {
	enabled bool
	reg     *registry.Registry

	// diags maps a document URI to its latest diagnostics. The embedded version
	// counter (bumped on every publishDiagnostics) is what WaitForDiagnostics
	// watches to decide the server has settled — no LSP progress tokens needed.
	diags *csync.VersionedMap[string, []protocol.Diagnostic]
	// openVersions tracks the last didOpen/didChange version sent per URI so
	// changes carry a monotonically increasing version.
	openVersions *csync.Map[string, int]
	// registered records clients we have already attached the diagnostics
	// handler to, so re-resolving a file does not double-register.
	registered *csync.Map[*plsp.Client, bool]
}

// New builds a Manager. When enabled is false every method is a no-op. It loads
// powernap's default server catalog; servers are started lazily on first use of
// a matching file (and only if their binary is on PATH).
func New(enabled bool) *Manager {
	m := &Manager{
		enabled:      enabled,
		diags:        csync.NewVersionedMap[string, []protocol.Diagnostic](),
		openVersions: csync.NewMap[string, int](),
		registered:   csync.NewMap[*plsp.Client, bool](),
	}
	if !enabled {
		return m
	}
	cfg := config.NewManager()
	_ = cfg.LoadDefaults() // best-effort: an empty catalog just means no servers
	reg := registry.New()
	_ = reg.LoadConfig(cfg)
	m.reg = reg
	return m
}

// Enabled reports whether language-server support is active.
func (m *Manager) Enabled() bool { return m.enabled && m.reg != nil }

// Notify tells the language server(s) for path that its content changed (or
// opens the document on first sight), so the server re-analyzes it. It is
// best-effort: an unsupported file type or a missing server is not an error —
// the agent's edit must not fail because no LSP is available.
func (m *Manager) Notify(ctx context.Context, path, content string) {
	if !m.Enabled() {
		return
	}
	clients, err := m.reg.GetClientsForFile(ctx, path)
	if err != nil || len(clients) == 0 {
		return // unsupported type, or no server on PATH
	}
	uri := pathToURI(path)
	prev, open := m.openVersions.Get(uri)
	version := prev + 1
	if !open {
		version = 1
	}
	m.openVersions.Set(uri, version)

	for _, c := range clients {
		m.ensureHandler(c)
		if !open {
			_ = c.NotifyDidOpenTextDocument(ctx, uri, string(plsp.DetectLanguage(path)), version, content)
			continue
		}
		_ = c.NotifyDidChangeTextDocument(ctx, uri, version, []protocol.TextDocumentContentChangeEvent{
			{Value: protocol.TextDocumentContentChangeWholeDocument{Text: content}},
		})
	}
}

// ensureHandler attaches the publishDiagnostics handler to a client exactly
// once. The handler stores the server's diagnostics keyed by URI and bumps the
// store version, which WaitForDiagnostics observes.
func (m *Manager) ensureHandler(c *plsp.Client) {
	if _, done := m.registered.Get(c); done {
		return
	}
	m.registered.Set(c, true)
	c.RegisterNotificationHandler(publishDiagnosticsMethod, func(_ context.Context, _ string, params json.RawMessage) {
		var p protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		m.diags.Set(string(p.URI), p.Diagnostics)
	})
}

// WaitForDiagnostics blocks until the language servers appear to have finished
// re-analyzing — the diagnostics version stops changing for settleQuiet — or
// until timeout / ctx cancellation. It infers "done" purely from version
// movement, so it works across servers without progress-token support.
func (m *Manager) WaitForDiagnostics(ctx context.Context, timeout time.Duration) {
	if !m.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := m.diags.Version()
	// Phase 1: wait for the first change (or give up after firstChangeWait).
	firstDeadline := time.After(firstChangeWait)
	for m.diags.Version() == start {
		select {
		case <-ctx.Done():
			return
		case <-firstDeadline:
			return // server emitted nothing; assume no diagnostics
		case <-time.After(settlePoll):
		}
	}
	// Phase 2: wait for the version to hold steady for settleQuiet.
	last := m.diags.Version()
	stable := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(settlePoll):
		}
		if v := m.diags.Version(); v != last {
			last = v
			stable = time.Now()
			continue
		}
		if time.Since(stable) >= settleQuiet {
			return
		}
	}
}

// Diagnostics returns the latest diagnostics for path (nil if none/unknown).
func (m *Manager) Diagnostics(path string) []protocol.Diagnostic {
	if !m.Enabled() {
		return nil
	}
	d, _ := m.diags.Get(pathToURI(path))
	return d
}

// All returns a snapshot of every file's diagnostics, keyed by filesystem path.
func (m *Manager) All() map[string][]protocol.Diagnostic {
	out := make(map[string][]protocol.Diagnostic)
	if !m.Enabled() {
		return out
	}
	m.diags.Range(func(uri string, diags []protocol.Diagnostic) bool {
		if len(diags) > 0 {
			out[uriToPath(uri)] = diags
		}
		return true
	})
	return out
}

// Shutdown stops all running language servers.
func (m *Manager) Shutdown(ctx context.Context) {
	if m.reg != nil {
		_ = m.reg.StopAll(ctx)
	}
}

// pathToURI converts a filesystem path to a file:// URI matching what the
// language server echoes back in publishDiagnostics (we open with this exact
// URI, so lookups are consistent).
func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed // Windows: /C:/...
	}
	return "file://" + slashed
}

// uriToPath is the inverse of pathToURI for display in the project report.
func uriToPath(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // /C:/... -> C:/...
	}
	return filepath.FromSlash(p)
}
