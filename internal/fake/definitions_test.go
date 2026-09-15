package fake

import (
	"strings"
	"testing"

	"github.com/infrena/infrena/pkg/schema"
)

// TestEveryTypeIsValidAndPrefixed. The host refuses a plugin on load for either failure,
// and the refusal names the plugin rather than the definition, so catch it here first.
// TestSchemasLoadThroughTheHost in protocol_test.go proves the same thing through the real host.
//
// ValidateAll, not only each Validate: a References names another type and attribute, and whether
// those exist is a fact about the whole set. infrena's host adapter runs ValidateAll on every load
// since v0.6.1, so TestSchemasLoadThroughTheHost would catch a dangling References too. This direct
// call stays anyway: it is cheap, and it fails right at the definitions, naming the attribute,
// rather than at a protocol load.
func TestEveryTypeIsValidAndPrefixed(t *testing.T) {
	defs := definitions()
	if len(defs) != 3 {
		t.Fatalf("got %d definitions, want 3", len(defs))
	}
	for _, d := range defs {
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", d.Type, err)
		}
		if !strings.HasPrefix(d.Type, PluginName+".") {
			t.Errorf("%s is not prefixed %s.", d.Type, PluginName)
		}
	}
	if err := schema.ValidateAll(defs); err != nil {
		t.Errorf("the definitions do not load as a set: %v", err)
	}
}

// TestOnlyIdentifierAttributesDeclareAReference. infrena PLAN.md §14.3: a References says an
// attribute HOLDS ANOTHER RESOURCE'S IDENTIFIER, and the engine projects `${res}` onto exactly the
// attribute named. So fake.database's network, which holds a fake.network's id, declares one, and
// nothing else does:
//
//   - fake.application's database_url holds a database's endpoint, a connection string rather than
//     an identifier. Declaring it would make `database_url: ${db}` mean the endpoint by a rule no
//     reader of the configuration can see, and would refuse a database_url built from anything but a
//     fake.database.
//   - tags is a map whose keys users choose, so it declares no Fields: nil is an open map, and a
//     Fields list would be a schema claiming to know a shape it does not.
func TestOnlyIdentifierAttributesDeclareAReference(t *testing.T) {
	want := map[string]schema.Reference{
		"fake.database.network": {Type: "fake.network", Attribute: "id"},
	}
	seen := 0
	for _, d := range definitions() {
		for name, a := range d.Attributes {
			key := d.Type + "." + name
			w, declared := want[key]
			switch {
			case declared && a.References == nil:
				t.Errorf("%s declares no References, want %+v", key, w)
			case declared && *a.References != w:
				t.Errorf("%s References = %+v, want %+v", key, *a.References, w)
			case !declared && a.References != nil:
				t.Errorf("%s declares References %+v, but it does not hold another resource's identifier", key, *a.References)
			}
			if declared {
				seen++
			}
			if a.Fields != nil {
				t.Errorf("%s declares Fields %v; no fake attribute is a map whose keys the plugin knows", key, a.Fields)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of the %d attributes expected to declare a reference", seen, len(want))
	}
}
