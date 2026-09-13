package fake

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/infrata/infrata/pkg/value"
)

// TestCreateDoesNotReplaceAHandAddedResource. Hand-editing the cloud file is supported, so a
// person can add `net-1` before `next_id` has reached it. The next create must choose a free ID,
// not silently overwrite infrastructure someone put there on purpose.
func TestCreateDoesNotReplaceAHandAddedResource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	hand := &Cloud{Resources: map[string]*CloudResource{
		"net-1": {Type: "fake.network", Attributes: map[string]any{"cidr": "10.9.0.0/16", "id": "net-1"}},
	}}
	if err := hand.Save(path); err != nil {
		t.Fatal(err)
	}

	st, err := New(path).Create(context.Background(), desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.ProviderID == "net-1" {
		t.Fatalf("Create reused net-1, which a person had already added by hand")
	}

	c, err := LoadCloud(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources) != 2 {
		t.Errorf("cloud holds %d resources after one create beside one hand-added resource, want 2", len(c.Resources))
	}
	if got := c.Resources["net-1"].Attributes["cidr"]; got != "10.9.0.0/16" {
		t.Errorf("the hand-added net-1 now has cidr %v, want 10.9.0.0/16 — it was overwritten", got)
	}
}
