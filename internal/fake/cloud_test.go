package fake

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCloudMissingFileIsEmptyNotError(t *testing.T) {
	c, err := LoadCloud(filepath.Join(t.TempDir(), "fake-cloud.json"))
	if err != nil {
		t.Fatalf("LoadCloud on a missing file: %v", err)
	}
	if len(c.Resources) != 0 {
		t.Errorf("expected an empty cloud, got %d resources", len(c.Resources))
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	c := &Cloud{Resources: map[string]*CloudResource{
		"db-1": {Type: "fake.database", Attributes: map[string]any{"engine": "postgres", "size": float64(10)}},
	}}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	if got.Resources["db-1"].Attributes["engine"] != "postgres" {
		t.Errorf("round trip lost data: %#v", got.Resources["db-1"])
	}
}

func TestSaveIsHumanEditable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	c := &Cloud{Resources: map[string]*CloudResource{"db-1": {Type: "fake.database"}}}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte("\n  ")) {
		t.Error("the cloud file must be indented — a human edits it to induce drift (PLAN.md §48)")
	}
}

func TestShouldFailMatchesNthOccurrence(t *testing.T) {
	c := &Cloud{Failures: []FailureRule{{Op: "create", Address: "db", Nth: 2, Message: "boom"}}}

	if _, ok := c.ShouldFail("create", "db"); ok {
		t.Error("first attempt must not fail when Nth is 2")
	}
	rule, ok := c.ShouldFail("create", "db")
	if !ok || rule.Message != "boom" {
		t.Fatalf("second attempt should fail, got ok=%v rule=%#v", ok, rule)
	}
	if _, ok := c.ShouldFail("create", "db"); ok {
		t.Error("a rule fires once, then stops")
	}
}

func TestShouldFailIgnoresOtherOpsAndAddresses(t *testing.T) {
	c := &Cloud{Failures: []FailureRule{{Op: "delete", Address: "db", Nth: 1}}}
	if _, ok := c.ShouldFail("create", "db"); ok {
		t.Error("rule must not match a different operation")
	}
	if _, ok := c.ShouldFail("delete", "other"); ok {
		t.Error("rule must not match a different address")
	}
}

// TestSaveIsAtomicAndPrivate mirrors internal/state's guard on the same
// discipline: mode 0600, and no temporary file left behind.
func TestSaveIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "fake-cloud.json")
	c := &Cloud{Resources: map[string]*CloudResource{"db-1": {Type: "fake.database"}}}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cloud file mode = %v, want 0600", info.Mode().Perm())
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Errorf("Save left %s behind", e.Name())
		}
	}
}

// TestUnknownRetryabilityIsRejected keeps a typo in a hand-edited file loud. Silently
// treating "sfe" as not-safe is a rule whose retry behaviour is never what the person
// editing the file intended, and nothing would say so.
func TestUnknownRetryabilityIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	doc := `{"resources": {}, "failures": [{"op": "create", "address": "db", "retryability": "sfe"}]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write cloud: %v", err)
	}
	_, err := LoadCloud(path)
	if err == nil {
		t.Fatal("LoadCloud accepted an unrecognised retryability")
	}
	if !strings.Contains(err.Error(), "sfe") {
		t.Errorf("error does not name the offending value: %v", err)
	}
}
