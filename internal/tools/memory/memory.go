// Package memory implements the Remember tool: a writing tool that saves a short
// note or fact to persistent memory so it can be recalled in future sessions.
//
// Conceptual mapping: the reference CLI's persistent-memory write action becomes
// package memory exporting tool.Tool via memory.New. Entries are stored in a
// caller-supplied internal/memory.Store; the tool never touches user files.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Input is the structured input for the Remember tool. Only Note is required —
// the bare {"note": "..."} form stores a plain note, and every other field is
// an optional refinement, so existing callers keep working unchanged.
type Input struct {
	// Note is the text to remember (for kind=fact it is the fact's value).
	Note string `json:"note" jsonschema:"required,description=The text to remember. For kind=fact this is the fact's value."`
	// Kind selects the entry type: "note" (default) or "fact".
	Kind string `json:"kind,omitempty" jsonschema:"description=Entry type: 'note' (default; free text recalled by relevance) or 'fact' (a key/value entry; requires key)."`
	// Key is the fact's key; setting an existing key replaces its value.
	Key string `json:"key,omitempty" jsonschema:"description=Fact key (kind=fact). Setting an existing key replaces its value."`
	// Pinned forces the entry into every future prompt.
	Pinned bool `json:"pinned,omitempty" jsonschema:"description=Always inject this entry into context instead of gating it by relevance. Use sparingly."`
	// Keywords gate an unpinned fact: it is injected only when one matches.
	Keywords []string `json:"keywords,omitempty" jsonschema:"description=For facts: inject the fact only when the user's message contains one of these words."`
	// Tags label a note to boost recall on related topics.
	Tags []string `json:"tags,omitempty" jsonschema:"description=For notes: topic tags that boost recall relevance."`
}

// Tool implements tool.Tool for saving entries to persistent memory.
type Tool struct {
	store *memory.Store
}

// New returns a new Remember tool backed by the given persistent memory store.
func New(store *memory.Store) *Tool {
	return &Tool{store: store}
}

// Name returns "Remember".
func (t *Tool) Name() string { return "Remember" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Saves a short note or fact to persistent memory so it can be recalled in future sessions. " +
		"Plain notes resurface when relevant to the conversation; facts (kind=fact with a key) are stable " +
		"key/value entries — pinned or keyword-gated. Writes are capped per turn, so record only what matters."
}

// InputSchema returns the JSON schema for Remember's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — Remember writes to the persistent memory file.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — writes to the memory file are serialized.
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission always allows Remember regardless of permission mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	// Remember only writes to the managed memory file, never to user files, so
	// it is safe to allow without prompting in every permission mode.
	_ = mode
	return perm.DecisionAllow
}

// Execute saves the entry to persistent memory. Invalid input JSON is a hard
// error; an empty note, a bad kind/key combination, a per-turn cap hit, or a
// store write failure is a soft error returned as a Result with IsError set.
// It honors ctx cancellation and never prints.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("memory: invalid input: %w", err)
	}
	if in.Note == "" {
		return tool.Result{Content: "note is required", IsError: true}, nil
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	// kind defaults from the presence of a key so the model can omit it.
	isFact := in.Kind == "fact" || (in.Kind == "" && in.Key != "")
	switch {
	case in.Kind != "" && in.Kind != "fact" && in.Kind != "note":
		return tool.Result{Content: fmt.Sprintf("unknown kind %q (want fact or note)", in.Kind), IsError: true}, nil
	case isFact && in.Key == "":
		return tool.Result{Content: "kind=fact requires a key", IsError: true}, nil
	case !isFact && in.Key != "":
		return tool.Result{Content: "key is only valid with kind=fact", IsError: true}, nil
	}

	var err error
	if isFact {
		err = t.store.SetFact(memory.Fact{
			Key:      in.Key,
			Value:    in.Note,
			Pinned:   in.Pinned,
			Keywords: in.Keywords,
		})
	} else {
		err = t.store.AddNote(memory.Note{Text: in.Note, Tags: in.Tags, Pinned: in.Pinned})
	}
	switch {
	case errors.Is(err, memory.ErrTurnCap):
		return tool.Result{
			Content: "not saved: " + err.Error() + "; consolidate what matters into fewer entries",
			IsError: true,
		}, nil
	case err != nil:
		return tool.Result{Content: fmt.Sprintf("cannot remember: %v", err), IsError: true}, nil
	case isFact:
		return tool.Result{Content: "remembered (fact " + in.Key + ")"}, nil
	default:
		return tool.Result{Content: "remembered"}, nil
	}
}
