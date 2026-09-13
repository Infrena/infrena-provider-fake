package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/infrata/infrata/pkg/address"
	"github.com/infrata/infrata/pkg/resource"
	"github.com/infrata/infrata/pkg/value"
)

func newTestProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	return New(path), path
}

func desired(name, resourceType string, attrs map[string]value.Value) *resource.DesiredResource {
	return &resource.DesiredResource{
		Address: address.Address{Name: name},
		Type:    resourceType,
		Attrs:   attrs,
	}
}

func TestCreateAssignsProviderIDAndComputedAttributes(t *testing.T) {
	p, _ := newTestProvider(t)
	st, err := p.Create(context.Background(), desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.ProviderID != "db-1" {
		t.Errorf("ProviderID = %q, want db-1", st.ProviderID)
	}
	if got, _ := st.Attributes["endpoint"].AsString(); got != "db-1.db.fake" {
		t.Errorf("endpoint = %q, want db-1.db.fake", got)
	}
}

func TestReadReflectsExternalMutation(t *testing.T) {
	p, path := newTestProvider(t)
	st, err := p.Create(context.Background(), desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mutate the cloud the way a human would, by editing the file.
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	c.Resources[st.ProviderID].Attributes["engine"] = "mysql"
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := p.Read(context.Background(), st)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s, _ := got.Attributes["engine"].AsString(); s != "mysql" {
		t.Errorf("engine = %q, want \"mysql\" — Read must observe external mutation, which is how drift is demonstrated", s)
	}
}

func TestReadReturnsNilWhenDeletedExternally(t *testing.T) {
	p, path := newTestProvider(t)
	st, _ := p.Create(context.Background(), desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))

	c, _ := LoadCloud(path)
	delete(c.Resources, st.ProviderID)
	_ = c.Save(path)

	got, err := p.Read(context.Background(), st)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != nil {
		t.Error("Read must return a nil state, and no error, for a resource that no longer exists")
	}
}

func TestUpdateAndDelete(t *testing.T) {
	p, _ := newTestProvider(t)
	ctx := context.Background()
	st, _ := p.Create(ctx, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
		"size":   value.Int(10, value.SourceDefault),
	}))

	updated, err := p.Update(ctx, st, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
		"size":   value.Int(50, value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got, _ := updated.Attributes["size"].AsInt(); got != 50 {
		t.Errorf("size = %d, want 50", got)
	}
	if updated.ProviderID != st.ProviderID {
		t.Error("Update must not change the provider ID")
	}

	if err := p.Delete(ctx, updated); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := p.Read(ctx, updated)
	if err != nil || gone != nil {
		t.Errorf("after Delete, Read = %v, %v; want nil, nil", gone, err)
	}
}

func TestNullAttributeIsTreatedAsUnset(t *testing.T) {
	p, path := newTestProvider(t)
	ctx := context.Background()
	st, err := p.Create(ctx, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Someone hand-edits the cloud file and nulls an attribute out.
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	c.Resources[st.ProviderID].Attributes["password"] = nil
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := p.Read(ctx, st)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v, ok := got.Attributes["password"]; ok {
		t.Errorf("a null attribute must be absent, not present as %#v", v)
	}
}

// TestCompositeAttributesRoundTripAsPlainJSON asserts the cloud file holds
// plain JSON for a composite attribute.
//
// Writing v.Raw directly serialises []value.Value or map[string]value.Value
// through Value.MarshalJSON, so the file gets the engine's internal wire
// objects. That defeats spec §8.4's premise that a human can hand-edit fake
// infrastructure, and reading it back yields a Map whose every leaf is itself a
// four-key kind/known/raw/source Map — which presents in M3 as inexplicable
// permanent drift rather than an obvious serialisation bug.
func TestCompositeAttributesRoundTripAsPlainJSON(t *testing.T) {
	p, path := newTestProvider(t)
	tags := value.Map(map[string]value.Value{
		"env":   value.String("dev", value.SourceExplicit),
		"tier":  value.Int(2, value.SourceExplicit),
		"inner": value.List([]value.Value{value.String("a", value.SourceExplicit)}, value.SourceExplicit),
	}, value.SourceExplicit)

	st, err := p.Create(context.Background(), desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
		"tags":   tags,
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	requirePlainTags := func(t *testing.T, when string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if bytes.Contains(data, []byte(`"kind"`)) || bytes.Contains(data, []byte(`"source"`)) {
			t.Errorf("cloud file after %s contains the engine's internal wire shape:\n%s", when, data)
		}
		var doc struct {
			Resources map[string]struct {
				Attributes map[string]any `json:"attributes"`
			} `json:"resources"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("unmarshal cloud file: %v", err)
		}
		got, ok := doc.Resources[st.ProviderID].Attributes["tags"].(map[string]any)
		if !ok {
			t.Fatalf("tags is %T, want a plain JSON object", doc.Resources[st.ProviderID].Attributes["tags"])
		}
		if got["env"] != "dev" {
			t.Errorf(`tags.env after %s = %#v, want "dev"`, when, got["env"])
		}
	}
	requirePlainTags(t, "Create")

	// And back out again, with no nesting garbage.
	requireLeaves := func(t *testing.T, v value.Value, when string) {
		t.Helper()
		m, ok := v.Raw.(map[string]value.Value)
		if !ok {
			t.Fatalf("tags after %s is %T, want map[string]value.Value", when, v.Raw)
		}
		if got, ok := m["env"].AsString(); !ok || got != "dev" {
			t.Errorf("tags.env after %s = %#v; want the string dev, not a re-wrapped wire object", when, m["env"])
		}
		if got, ok := m["tier"].AsInt(); !ok || got != 2 {
			t.Errorf("tags.tier after %s = %#v, want 2", when, m["tier"])
		}
		items, ok := m["inner"].Raw.([]value.Value)
		if !ok || len(items) != 1 {
			t.Fatalf("tags.inner after %s = %#v, want a one-element list", when, m["inner"])
		}
		if got, ok := items[0].AsString(); !ok || got != "a" {
			t.Errorf("tags.inner[0] after %s = %#v, want the string a", when, items[0])
		}
	}
	requireLeaves(t, st.Attributes["tags"], "Create")

	read, err := p.Read(context.Background(), st)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	requireLeaves(t, read.Attributes["tags"], "Read")

	updated, err := p.Update(context.Background(), st, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
		"tags":   tags,
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	requirePlainTags(t, "Update")
	requireLeaves(t, updated.Attributes["tags"], "Update")
}

// TestUpdateRemovesAnAttributeTheConfigurationDropped. providers/test merged desired
// attributes and never deleted one: removing `tags:` planned `tags -> (absent)`, apply
// reported success, the cloud kept the tags, and every later plan proposed the same
// update forever. Computed attributes are never in the desired state, so they must survive.
func TestUpdateRemovesAnAttributeTheConfigurationDropped(t *testing.T) {
	p, path := newTestProvider(t)
	ctx := context.Background()
	st, err := p.Create(ctx, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
		"tags": value.Map(map[string]value.Value{
			"team": value.String("data", value.SourceExplicit),
		}, value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := p.Update(ctx, st, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := updated.Attributes["tags"]; ok {
		t.Error("Update returned tags, which the desired state no longer has")
	}
	if _, ok := updated.Attributes["endpoint"]; !ok {
		t.Error("Update dropped the computed endpoint, which desired state never carries")
	}

	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	if _, ok := c.Resources[st.ProviderID].Attributes["tags"]; ok {
		t.Error("the cloud file still holds tags: the next plan would propose removing them again")
	}
}

// TestResultsCarryOnlyWhatThePluginOwns. The host re-attaches address, instance,
// dependencies, lifecycle and timestamps, and ignores them on a result. A plugin that sets
// them anyway is reimplementing a host rule, and its tests would pass with that rule broken.
func TestResultsCarryOnlyWhatThePluginOwns(t *testing.T) {
	p, _ := newTestProvider(t)
	st, err := p.Create(context.Background(), desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.Address.String() != "" || st.Provider != "" || !st.CreatedAt.IsZero() || !st.UpdatedAt.IsZero() {
		t.Errorf("Create set host bookkeeping: address=%q provider=%q created=%v updated=%v",
			st.Address, st.Provider, st.CreatedAt, st.UpdatedAt)
	}
}
