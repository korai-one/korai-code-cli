// Package tools wires the built-in tool set into a registry.
//
// This file is the coordinator-owned registration list (AGENTS.md §5/§8).
// Each tool lives in its own subpackage; this is the single place they are
// assembled, so adding a tool is a one-line edit here with no cross-package
// global mutation.
package tools

import (
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/applypatch"
	"github.com/Nevaero/korai-code-cli/internal/tools/bash"
	"github.com/Nevaero/korai-code-cli/internal/tools/edit"
	"github.com/Nevaero/korai-code-cli/internal/tools/glob"
	"github.com/Nevaero/korai-code-cli/internal/tools/grep"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
	"github.com/Nevaero/korai-code-cli/internal/tools/repomap"
	"github.com/Nevaero/korai-code-cli/internal/tools/webfetch"
	"github.com/Nevaero/korai-code-cli/internal/tools/write"
)

// RegisterAll registers every built-in tool into r.
//
// WebSearch is intentionally NOT registered: it has no provider wired
// (WithSearcher is only used in tests), so it would always return "web search is
// not available" while still being advertised to the model, wasting turns. The
// package stays in the tree so it can be registered here the moment a search
// backend is configured.
func RegisterAll(r *tool.Registry) {
	r.Register(readfile.New())
	r.Register(write.New())
	r.Register(edit.New())
	r.Register(applypatch.New())
	r.Register(bash.New())
	r.Register(grep.New())
	r.Register(glob.New())
	r.Register(repomap.New())
	r.Register(webfetch.New())
}
