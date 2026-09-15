package fake

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/infrena/infrena/pkg/address"
	"github.com/infrena/infrena/pkg/plugintest"
	"github.com/infrena/infrena/pkg/provider"
	"github.com/infrena/infrena/pkg/resource"
	"github.com/infrena/infrena/pkg/schema"
	"github.com/infrena/infrena/pkg/value"
)

// openHost connects this plugin to infrena's own host over an in-memory pipe, so every call
// is encoded, decoded and passed through the trust rules exactly as it is from a subprocess.
func openHost(t *testing.T) (*plugintest.Host, string) {
	t.Helper()
	dir := t.TempDir()
	host, err := plugintest.Open(context.Background(), NewPlugin(), dir)
	if err != nil {
		t.Fatalf("the host refused this plugin's schemas: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host, dir
}

// TestSchemasLoadThroughTheHost. Open fails for a type outside the `fake.` prefix, a reserved
// attribute name, or a default of the wrong kind. The size default is checked explicitly: it
// is the one value in a schema that has to survive JSON as an integer.
func TestSchemasLoadThroughTheHost(t *testing.T) {
	host, _ := openHost(t)
	if got := host.Version(); got != Version {
		t.Errorf("handshake version = %q, want %q", got, Version)
	}
	var db bool
	for _, d := range host.Definitions() {
		if d.Type != "fake.database" {
			continue
		}
		db = true
		a, _ := d.Attribute("size")
		if n, ok := a.Default.(int64); !ok || n != 10 {
			t.Errorf("size default after the wire = %#v (%T), want int64(10)", a.Default, a.Default)
		}
	}
	if !db {
		t.Fatal("fake.database did not arrive")
	}
}

// TestADeclaredReferenceCrossesTheProtocol. References is protocol 3 (infrena PLAN.md §14.3): the
// schema payload gained a key that an attribute decodes leniently, so a References lost on the wire
// would not fail anything here. It would only make `network: ${network}` a "declares no reference"
// compile error for a user. So assert the declaration as the host received it, not as the plugin
// built it.
//
// ValidateAll runs on what arrived because Open does not run it: pluginhost checks each definition
// alone, and the whole-set check (a References naming a type or attribute nobody serves) runs in
// infrena's registry, when the CLI registers the plugin.
func TestADeclaredReferenceCrossesTheProtocol(t *testing.T) {
	host, _ := openHost(t)
	defs := host.Definitions()
	if err := schema.ValidateAll(defs); err != nil {
		t.Fatalf("the schemas the host received do not load as a set: %v", err)
	}
	var found bool
	for _, d := range defs {
		if d.Type != "fake.database" {
			continue
		}
		a, _ := d.Attribute("network")
		if a.References == nil {
			t.Fatal("fake.database's network arrived with no References: it was dropped on the wire")
		}
		if want := (schema.Reference{Type: "fake.network", Attribute: "id"}); *a.References != want {
			t.Errorf("network References after the wire = %+v, want %+v", *a.References, want)
		}
		found = true
	}
	if !found {
		t.Fatal("fake.database did not arrive")
	}
}

// TestACreateRoundTripsThroughTheHost, landing the implicit instance's cloud where the
// project already keeps it.
func TestACreateRoundTripsThroughTheHost(t *testing.T) {
	host, dir := openHost(t)
	prov, err := host.Configure(provider.Config{Instance: PluginName})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	ctx := context.Background()
	st, err := prov.Create(ctx, &resource.DesiredResource{
		Address: address.Address{Name: "net"},
		Type:    "fake.network",
		Attrs:   map[string]value.Value{"cidr": value.String("10.0.0.0/16", value.SourceExplicit)},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.Address.String() != "net" || st.Provider != PluginName {
		t.Errorf("host did not re-attach bookkeeping: address=%q provider=%q", st.Address, st.Provider)
	}
	got, err := prov.Read(ctx, st)
	if err != nil || got == nil {
		t.Fatalf("Read = %v, %v", got, err)
	}
	if id, _ := got.Attributes["id"].AsString(); id != st.ProviderID {
		t.Errorf("id = %q, want %q", id, st.ProviderID)
	}
	if _, err := LoadCloud(filepath.Join(dir, DefaultCloudPath)); err != nil {
		t.Errorf("the implicit instance's cloud is not at %s: %v", DefaultCloudPath, err)
	}
}

// TestADiscoveredSecretIsRedactedByTheHost. This plugin deliberately does NOT mark sensitive
// values, because the host forces sensitivity from the schema. This is the test that says so end to end.
func TestADiscoveredSecretIsRedactedByTheHost(t *testing.T) {
	host, dir := openHost(t)
	if err := (&Cloud{Resources: map[string]*CloudResource{
		"db-9": {Type: "fake.database", Attributes: map[string]any{"engine": "postgres", "password": "hunter2"}},
	}}).Save(filepath.Join(dir, DefaultCloudPath)); err != nil {
		t.Fatal(err)
	}
	prov, err := host.Configure(provider.Config{Instance: PluginName})
	if err != nil {
		t.Fatal(err)
	}
	found, err := prov.Discover(context.Background(), provider.DiscoverRequest{})
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover = %v, %v", found, err)
	}
	if !found[0].Attributes["password"].Sensitive {
		t.Error("a discovered password reached the engine unmarked")
	}
}

// TestAnInjectedFailureKeepsItsClassificationAcrossThePipe. An error value cannot cross a
// pipe; the SDK classifies on this side and the class travels with the message.
func TestAnInjectedFailureKeepsItsClassificationAcrossThePipe(t *testing.T) {
	host, dir := openHost(t)
	if err := (&Cloud{
		Resources: map[string]*CloudResource{},
		Failures:  []FailureRule{{Op: "create", Address: "net", Nth: 1, Retryability: RetrySafe, Message: "throttled"}},
	}).Save(filepath.Join(dir, DefaultCloudPath)); err != nil {
		t.Fatal(err)
	}
	prov, err := host.Configure(provider.Config{Instance: PluginName})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prov.Create(context.Background(), &resource.DesiredResource{
		Address: address.Address{Name: "net"}, Type: "fake.network",
		Attrs: map[string]value.Value{"cidr": value.String("10.0.0.0/16", value.SourceExplicit)},
	})
	if err == nil {
		t.Fatal("expected the injected failure")
	}
	if got := prov.ClassifyError(err); got != provider.SafeToRetry {
		t.Errorf("classification after the pipe = %v, want SafeToRetry", got)
	}
}
