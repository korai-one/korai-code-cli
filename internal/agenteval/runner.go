package agenteval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Run executes one offline scenario in workDir: it writes the fixture files,
// builds the real engine over the scripted client and the scenario's real
// tools, drives the run to completion, and returns the trace-derived Result.
// The returned error covers harness failures only (fixture setup); engine-side
// failures — including running past the script — land in Result.Err so a test
// can assert on them.
func Run(ctx context.Context, sc Scenario, workDir string) (Result, error) {
	if err := writeFixtures(workDir, sc.Files); err != nil {
		return Result{}, err
	}

	rec := &recordingClient{inner: &scriptClient{turns: sc.Turns}}

	var tools []tool.Tool
	var opts []engine.Option
	if sc.Setup != nil {
		tools, opts = sc.Setup(workDir)
	}
	registry := tool.NewRegistry()
	for _, t := range tools {
		registry.Register(t)
	}

	asker := sc.Asker
	if asker == nil {
		asker = perm.DenyAsker{}
	}
	permEngine := perm.NewEngine(perm.NewModeSelector(sc.Mode), perm.Rules{}, asker)

	eng := engine.New(rec, registry, permEngine, tool.Deps{WorkDir: workDir}, opts...)

	messages := make([]apiclient.Message, 0, len(sc.History)+1)
	messages = append(messages, sc.History...)
	messages = append(messages, apiclient.Message{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: sc.Prompt}},
	})

	col := newCollector(workDir)
	for evt := range eng.Run(ctx, messages, sc.System) {
		col.observe(evt)
	}
	return col.finish(rec.requests()), nil
}

// writeFixtures materializes the scenario's fixture files under workDir,
// creating parent directories as needed.
func writeFixtures(workDir string, files map[string]string) error {
	for rel, content := range files {
		full := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("agenteval: creating fixture dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return fmt.Errorf("agenteval: writing fixture %s: %w", rel, err)
		}
	}
	return nil
}
