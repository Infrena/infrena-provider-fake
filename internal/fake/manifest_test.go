package fake

import (
	"os"
	"testing"

	"github.com/infrena/infrena/pkg/pluginmanifest"
	"github.com/infrena/infrena/pkg/pluginproto"
)

// readManifest parses the repository's plugin.yaml with infrena's own parser: the same code
// `infrena plugins install` runs against it, so a manifest that passes here is one
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

// TestTheManifestDescribesThisPlugin. The manifest is authoritative for a release, so it
// must name the plugin this binary is and list exactly the protocol this SDK speaks. The version is not
// compared here: the code reports 0.0.0-dev until a release stamps it, and scripts/release-check
// is what asserts tag == manifest == binary.
//
// Exactly, not "includes": infrena reads `protocol:` as the versions this
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

// TestTheManifestFloorIsTheRequiredRelease. The floor is the release go.mod requires and CI tests.
//
// 0.13.0 SINCE 2026-09-18. Two reasons, and the second is the one that made it exact rather
// than cautious. 0.12.0 raised the plugin protocol to 5, which this binary speaks and every
// earlier host refuses at the handshake. Then every release before 0.13.0 was DELETED, tags
// included, as stale: a floor may only name a release that exists, and the oldest one that
// does is 0.13.0.
//
// 0.12.0 was also the release in which `plan` and `apply` discarded the `providers:` block
// and ran against whatever account the default credential chain pointed at, so no floor
// should have admitted it anyway.
//
// The versions below are about the protocol, which is the only thing that decides whether a
// host can run this binary at all. Checked with release versions, which AllowsInfrena does not
// exempt the way it does 0.0.0.
func TestTheManifestFloorIsTheRequiredRelease(t *testing.T) {
	m := readManifest(t)
	if m.Infrena.IsZero() {
		t.Fatal("plugin.yaml has no infrena: floor, so it claims to work with pre-rename releases too")
	}
	for version, want := range map[string]bool{
		"0.3.9": false, "0.5.9": false, "0.6.2": false,
		// Protocol 4 hosts: fine for the previous release of this plugin, and
		// too old for this one.
		"0.7.0": false, "0.11.1": false,
		// Protocol 5, but deleted as stale and below the floor.
		"0.12.0": false, "0.12.9": false,
		// The oldest release that still exists, and everything after it.
		"0.14.0": true, "0.14.9": true, "1.0.0": true,
	} {
		if got := m.AllowsInfrena(version); got != want {
			t.Errorf("plugin.yaml's infrena: %q allows %s = %v, want %v", m.Infrena, version, got, want)
		}
	}
}
