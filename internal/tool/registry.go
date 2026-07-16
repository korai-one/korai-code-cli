package tool

import (
	"fmt"
	"sort"
)

// Registry holds the set of tools available to the engine.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds t to the registry. Panics on duplicate names — registration
// happens at startup and a duplicate is always a programming error.
func (r *Registry) Register(t Tool) {
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tool %q registered twice", t.Name()))
	}
	r.tools[t.Name()] = t
}

// Get returns the tool with the given name and whether it was found.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// All returns every registered tool, sorted by name so the order presented to
// the model (in the request's tool list) and to tests is deterministic across
// runs rather than dependent on Go's map iteration order.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
