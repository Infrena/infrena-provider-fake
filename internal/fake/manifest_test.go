package fake

import (
	"os"
	"testing"

	"github.com/infrata/infrata/pkg/pluginmanifest"
	"github.com/infrata/infrata/pkg/pluginproto"
)

// readManifest parses the repository's plugin.yaml with infrata's own parser: the same code
// `infrata plugins install` runs against it (PLAN.md §31.2), so a manifest that passes here is one
// install will accept rather than one a second parser merely agreed with.
func readManifest(t *testing.T) *pluginmanifest.Manifest {
	t.Helper()
	data, err := os.ReadFile("../../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, warnings, err := pluginmanifest.Parse(data)
	if err != nil {
		t.Fatalf("plugin.yaml is not a manifest infrata accepts: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("plugin.yaml parses with warnings, which install would print: %v", warnings)
	}
	return m
}

// TestTheManifestDescribesThisPlugin. The manifest is authoritative for a release (§31.2), so it
// must name the plugin this binary is and speak the protocol this SDK speaks. The version is not
// compared here: the code reports 0.0.0-dev until a release stamps it, and scripts/release-check
// is what asserts tag == manifest == binary.
func TestTheManifestDescribesThisPlugin(t *testing.T) {
	m := readManifest(t)
	if m.Name != PluginName {
		t.Errorf("plugin.yaml names %q, but this plugin is %q", m.Name, PluginName)
	}
	if !m.SpeaksProtocol([]int{pluginproto.Version}) {
		t.Errorf("plugin.yaml's protocol %v does not include protocol %d, which the SDK this plugin is built with speaks",
			m.Protocol, pluginproto.Version)
	}
}
