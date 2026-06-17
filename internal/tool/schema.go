package tool

import "github.com/invopop/jsonschema"

// Schema generates an inlined JSON Schema for the tool input type T.
//
// Every tool must use this helper rather than configuring its own reflector,
// so the whole tool set produces schemas in one consistent shape. The schema
// is inlined (no $ref/$defs) because the inference API expects a self-contained
// object with top-level "properties", "required", and "type":"object".
func Schema[T any]() *jsonschema.Schema {
	r := &jsonschema.Reflector{
		RequiredFromJSONSchemaTags: true,
		DoNotReference:             true,
		ExpandedStruct:             true,
	}
	var zero T
	s := r.Reflect(&zero)
	// Strip metadata the API neither needs nor accepts at the top level.
	s.Version = ""
	s.ID = ""
	return s
}
