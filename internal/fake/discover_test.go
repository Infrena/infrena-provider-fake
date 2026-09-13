package fake

import (
	"context"
	"strings"
	"testing"

	"github.com/infrata/infrata/pkg/provider"
)

// writeCloud writes a cloud file directly, so a test can describe
// infrastructure this tool never created — which is the whole subject of
// discovery.
func writeCloud(t *testing.T, path string, c *Cloud) {
	t.Helper()
	if err := c.Save(path); err != nil {
		t.Fatalf("writing the cloud: %v", err)
	}
}

// preexisting is a cloud holding two resources with no `address` — nothing here
// was created by this project, which is the case discovery exists for.
func preexisting() *Cloud {
	return &Cloud{Resources: map[string]*CloudResource{
		"net-1": {Type: "fake.network", Attributes: map[string]any{
			"cidr": "10.0.0.0/16", "id": "net-1",
		}},
		"db-9": {Type: "fake.database", Attributes: map[string]any{
			"engine": "postgres", "id": "db-9", "password": "hunter2",
		}},
	}}
}

// TestDiscoverFindsEveryResourceInTheCloud, including ones this project never
// created — which is the entire point: discovery is for infrastructure that
// predates the tool. Neither fixture resource carries an `address`.
func TestDiscoverFindsEveryResourceInTheCloud(t *testing.T) {
	p, path := newTestProvider(t)
	writeCloud(t, path, preexisting())

	got, err := p.Discover(context.Background(), provider.DiscoverRequest{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d resources, want 2: %+v", len(got), got)
	}
	// Sorted by provider ID. The list is printed to a user and diffed by
	// scripts, and Go's map iteration is randomised — "db-9" before "net-1"
	// contradicts the order the fixture happens to be written in.
	if got[0].ProviderID != "db-9" || got[1].ProviderID != "net-1" {
		t.Errorf("discovery is not sorted: %s, %s", got[0].ProviderID, got[1].ProviderID)
	}
	if got[0].Type != "fake.database" {
		t.Errorf("got[0].Type = %q, want fake.database", got[0].Type)
	}
	if cidr, _ := got[1].Attributes["cidr"].AsString(); cidr != "10.0.0.0/16" {
		t.Errorf("net-1 cidr = %v, want the provider's value", got[1].Attributes["cidr"])
	}
}

// TestDiscoverIsDeterministic — map iteration is randomised, so one run proving
// the order proves nothing.
func TestDiscoverIsDeterministic(t *testing.T) {
	p, path := newTestProvider(t)
	c := preexisting()
	for _, id := range []string{"alpha", "zeta", "mid", "beta"} {
		c.Resources[id] = &CloudResource{Type: "fake.network", Attributes: map[string]any{"id": id}}
	}
	writeCloud(t, path, c)

	var first []string
	for i := 0; i < 20; i++ {
		got, err := p.Discover(context.Background(), provider.DiscoverRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range got {
			ids = append(ids, r.ProviderID)
		}
		if i == 0 {
			first = ids
			continue
		}
		if strings.Join(ids, ",") != strings.Join(first, ",") {
			t.Fatalf("run %d returned %v, want %v", i, ids, first)
		}
	}
	if strings.Join(first, ",") != "alpha,beta,db-9,mid,net-1,zeta" {
		t.Errorf("discovery order = %v, want sorted by provider ID", first)
	}
}

// TestDiscoverFiltersByType. §25's `infra discover aws.rds` asks one question of
// a large account, and answering it by fetching everything and discarding most
// is how discovery becomes too slow to use.
func TestDiscoverFiltersByType(t *testing.T) {
	p, path := newTestProvider(t)
	writeCloud(t, path, preexisting())

	got, err := p.Discover(context.Background(), provider.DiscoverRequest{Types: []string{"fake.database"}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Both halves. A count alone passes against a filter that returns nothing,
	// and a presence check alone passes against one that filters nothing.
	if len(got) != 1 || got[0].ProviderID != "db-9" {
		t.Fatalf("filtered discovery = %+v, want only db-9", got)
	}
	for _, r := range got {
		if r.Type == "fake.network" {
			t.Errorf("the filter returned a %s, which was not asked for", r.Type)
		}
	}
}

// TestDiscoverOnAnEmptyCloudFindsNothing — an account with nothing in it is not
// an error, and neither is a project that has never applied anything.
func TestDiscoverOnAnEmptyCloudFindsNothing(t *testing.T) {
	p, _ := newTestProvider(t)
	got, err := p.Discover(context.Background(), provider.DiscoverRequest{})
	if err != nil {
		t.Fatalf("discovering an empty cloud must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("found %d resources in an empty cloud", len(got))
	}
}

// TestImportReadsARealResourceByID — §26. Import adopts what already exists.
func TestImportReadsARealResourceByID(t *testing.T) {
	p, path := newTestProvider(t)
	writeCloud(t, path, preexisting())

	st, err := p.Import(context.Background(), "fake.database", "db-9")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if st.ProviderID != "db-9" {
		t.Errorf("ProviderID = %q, want db-9", st.ProviderID)
	}
	if st.Type != "fake.database" {
		t.Errorf("Type = %q, want fake.database", st.Type)
	}
	if engine, _ := st.Attributes["engine"].AsString(); engine != "postgres" {
		t.Errorf("engine = %v, want the provider's value", st.Attributes["engine"])
	}
}

// TestImportOfAnUnknownIDNamesTheID. A user importing by ID has usually
// mistyped it or is looking in the wrong account; an error that does not repeat
// the ID leaves them unable to tell which.
func TestImportOfAnUnknownIDNamesTheID(t *testing.T) {
	p, path := newTestProvider(t)
	writeCloud(t, path, preexisting())

	_, err := p.Import(context.Background(), "fake.database", "db-404")
	if err == nil {
		t.Fatal("importing an ID that does not exist must be an error")
	}
	if !strings.Contains(err.Error(), "db-404") {
		t.Errorf("the error does not name the ID: %v", err)
	}
}

// TestImportOfTheWrongTypeIsRefused. `import fake.network db-9` names a real
// resource of the wrong type. Adopting it anyway writes state claiming a
// database is a network, and the next plan proposes replacing real
// infrastructure to fix a disagreement the tool invented.
func TestImportOfTheWrongTypeIsRefused(t *testing.T) {
	p, path := newTestProvider(t)
	writeCloud(t, path, preexisting())

	_, err := p.Import(context.Background(), "fake.network", "db-9")
	if err == nil {
		t.Fatal("importing a resource as the wrong type must be an error")
	}
	for _, want := range []string{"db-9", "fake.network", "fake.database"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q — it must say what was asked for and what is "+
				"actually there: %v", want, err)
		}
	}
}
