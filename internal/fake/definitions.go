package fake

import (
	"github.com/infrena/infrena/pkg/schema"
	"github.com/infrena/infrena/pkg/value"
)

// definitions returns the resource definitions the fake provider supports.
// Their shapes are chosen to exercise the engine: fake.network has no
// dependencies, fake.database has a ForceNew attribute plus a computed one
// plus a sensitive one plus a composite one and requires a network, and
// fake.application requires a database.
func definitions() []*schema.ResourceDefinition {
	return []*schema.ResourceDefinition{
		{
			Type:        "fake.network",
			Description: "A fake network. Has no dependencies.",
			Attributes: map[string]schema.Attribute{
				"cidr": {Kind: value.KindString, Required: true, ForceNew: true, Description: "Address range"},
				"id":   {Kind: value.KindString, Computed: true, Description: "Assigned network identifier"},
			},
			Capabilities: schema.Capabilities{Create: true, Read: true, Update: true, Delete: true, Import: true},
			ImportID:     schema.ImportSpec{Description: "the network identifier, e.g. net-1"},
		},
		{
			Type:        "fake.database",
			Description: "A fake database. Requires a network.",
			Attributes: map[string]schema.Attribute{
				"engine": {Kind: value.KindString, Required: true, ForceNew: true, Description: "Database engine"},
				// A default is a datum, not a function: a schema has to survive a pipe.
				"size":     {Kind: value.KindInt, Description: "Storage in GB", Default: int64(10)},
				"password": {Kind: value.KindString, Sensitive: true, Description: "Administrator password"},
				// network holds a fake.network's identifier, so it says so: the declaration is
				// what lets configuration write `network: ${network}` and have infrena fill in
				// `.id`, and what makes `network: ${db.endpoint}` a compile error. The plugin
				// decides which attribute a reference means; infrena never guesses one.
				"network": {
					Kind:        value.KindString,
					Description: "Network this database sits in",
					References:  &schema.Reference{Type: "fake.network", Attribute: "id"},
				},
				// A composite attribute is deliberately present: without one,
				// nothing exercises the conversion between a typed Value and
				// the plain JSON the hand-editable cloud file must hold.
				"tags":     {Kind: value.KindMap, Description: "Free-form labels"},
				"endpoint": {Kind: value.KindString, Computed: true, Description: "Connection endpoint"},
			},
			Requirements: []schema.Requirement{{
				Name:        "network",
				Types:       []string{"fake.network"},
				Description: "A database must sit inside a network",
			}},
			Capabilities: schema.Capabilities{Create: true, Read: true, Update: true, Delete: true, Import: true},
			ImportID:     schema.ImportSpec{Description: "the database identifier, e.g. db-1"},
		},
		{
			Type:        "fake.application",
			Description: "A fake application. Requires a database.",
			Attributes: map[string]schema.Attribute{
				"image":        {Kind: value.KindString, Required: true, Description: "Container image"},
				"replicas":     {Kind: value.KindInt, Description: "Instance count", Default: int64(1)},
				"database_url": {Kind: value.KindString, Description: "Connection string"},
				"url":          {Kind: value.KindString, Computed: true, Description: "Public URL"},
			},
			Requirements: []schema.Requirement{{
				Name:        "database",
				Types:       []string{"fake.database"},
				Description: "An application must have a database",
			}},
			Capabilities: schema.Capabilities{Create: true, Read: true, Update: true, Delete: true, Import: true},
			ImportID:     schema.ImportSpec{Description: "the application identifier, e.g. app-1"},
		},
	}
}

// definitionOf returns the definition for a type, or nil for a type this plugin does not
// serve — which a hand-edited cloud file can contain.
func definitionOf(resourceType string) *schema.ResourceDefinition {
	for _, d := range definitions() {
		if d.Type == resourceType {
			return d
		}
	}
	return nil
}
