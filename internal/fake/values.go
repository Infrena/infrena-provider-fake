package fake

import (
	"fmt"

	"github.com/infrata/infrata/pkg/resource"
	"github.com/infrata/infrata/pkg/value"
)

// stateOf converts a cloud object into what a plugin reports: its type, its ID and its
// attributes, and nothing else.
//
// Deliberately NOT the address, the instance, dependencies, lifecycle or timestamps. The
// host never sends those and re-attaches them itself, and it forces sensitivity and
// provenance from the schema onto every value — so a plugin that also did any of it would
// be a plugin whose tests pass when the host is broken.
func stateOf(resourceType, id string, attrs map[string]any) *resource.ResourceState {
	out := make(map[string]value.Value, len(attrs))
	for name, raw := range attrs {
		// A JSON null means unset. Someone hand-editing the cloud file may null a value
		// out; turning it into the string "<nil>" would silently corrupt it.
		if raw == nil {
			continue
		}
		out[name] = fromRaw(raw)
	}
	return &resource.ResourceState{Type: resourceType, ProviderID: id, Attributes: out}
}

// toRaw converts a typed Value into the plain JSON datum the cloud file holds,
// the inverse of fromRaw.
//
// Writing v.Raw directly would work for scalars but serialise a composite's
// []value.Value or map[string]value.Value through Value.MarshalJSON, putting
// the engine's internal wire objects into a file a human is meant to hand-edit
// — and reading them back would produce nested garbage.
func toRaw(v value.Value) any {
	switch v.Kind {
	case value.KindList:
		items, _ := v.Raw.([]value.Value)
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, toRaw(item))
		}
		return out
	case value.KindMap:
		items, _ := v.Raw.(map[string]value.Value)
		out := make(map[string]any, len(items))
		for k, item := range items {
			out[k] = toRaw(item)
		}
		return out
	default:
		return v.Raw
	}
}

// fromRaw converts a JSON datum into a typed Value. JSON numbers arrive as
// float64; whole numbers become integers so they compare equal to configured
// integer attributes.
func fromRaw(raw any) value.Value {
	switch v := raw.(type) {
	case string:
		return value.String(v, value.SourceProvider)
	case bool:
		return value.Bool(v, value.SourceProvider)
	case float64:
		if v == float64(int64(v)) {
			return value.Int(int64(v), value.SourceProvider)
		}
		return value.Float(v, value.SourceProvider)
	case int64:
		return value.Int(v, value.SourceProvider)
	case []any:
		items := make([]value.Value, 0, len(v))
		for _, item := range v {
			items = append(items, fromRaw(item))
		}
		return value.List(items, value.SourceProvider)
	case map[string]any:
		items := map[string]value.Value{}
		for k, item := range v {
			items[k] = fromRaw(item)
		}
		return value.Map(items, value.SourceProvider)
	case nil:
		// Unreachable for a top-level attribute (toState skips nulls), but a
		// null nested inside a list or map lands here. Return the zero Value,
		// whose KindInvalid fails loudly downstream rather than masquerading
		// as the string "<nil>".
		return value.Value{}
	default:
		return value.String(fmt.Sprint(v), value.SourceProvider)
	}
}
