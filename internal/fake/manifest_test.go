package fake

import (
	"os"
	"testing"

	"github.com/infrena/infrena/pkg/pluginmanifest"
	"github.com/infrena/infrena/pkg/pluginproto"
)

// readManifest parses the repository's plugin.yaml with infrena's own parser: the same code
// `infrena plugins install` runs against it (infrena PLAN.md §31.2), so a manifest that passes here is one
// install will accept rather than one a second parser merely agreed with.
func readManifest(t *testing.T) *pluginmanifest.Manifest {
	t.Helper()
	data, err := os.ReadFile("../../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, warnings, err := pluginmanifest.Parse(data)
	if err != nil {
		t.Fatalf("plugin.yaml is not a manifest infrena accepts: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("plugin.yaml parses with warnings, which install would print: %v", warnings)
	}
	return m
}

// TestTheManifestDescribesThisPlugin. The manifest is authoritative for a release (infrena PLAN.md §31.2), so it
// must name the plugin this binary is and list exactly the protocol this SDK speaks. The version is not
// compared here: the code reports 0.0.0-dev until a release stamps it, and scripts/release-check
// is what asserts tag == manifest == binary.
//
// Exactly, not "includes": §31.2 (amended 2026-09-14) defines `protocol:` as the versions this
// release's binary speaks, and an SDK-built binary announces one. A manifest listing the host's
// whole Supported set, [2, 1], claims a protocol this binary cannot speak.
func TestTheManifestDescribesThisPlugin(t *testing.T) {
	m := readManifest(t)
	if m.Name != PluginName {
		t.Errorf("plugin.yaml names %q, but this plugin is %q", m.Name, PluginName)
	}
	if len(m.Protocol) != 1 || m.Protocol[0] != pluginproto.Version {
		t.Errorf("plugin.yaml's protocol is %v, but the SDK this plugin is built with speaks exactly [%d]; "+
			"change it in the same commit as go.mod's infrena require",
			m.Protocol, pluginproto.Version)
	}
}

// TestTheManifestFloorIsTheFirstRenamedRelease. Every infrena release before 0.4.0 was published
// as infrata, under another module path, CLI and plugin binary name, so no build of this plugin can
// pair with one. The floor must refuse the last of them and admit the first renamed release.
// Checked with release versions, which AllowsInfrena does not exempt the way it does 0.0.0.
func TestTheManifestFloorIsTheFirstRenamedRelease(t *testing.T) {
	m := readManifest(t)
	if m.Infrena.IsZero() {
		t.Fatal("plugin.yaml has no infrena: floor, so it claims to work with infrata-named releases too")
	}
	for version, want := range map[string]bool{"0.3.0": false, "0.3.9": false, "0.4.0": true, "0.5.0": true} {
		if got := m.AllowsInfrena(version); got != want {
			t.Errorf("plugin.yaml's infrena: %q allows %s = %v, want %v", m.Infrena, version, got, want)
		}
	}
}
