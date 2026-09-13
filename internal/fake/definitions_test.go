package fake

import (
	"strings"
	"testing"
)

// TestEveryTypeIsValidAndPrefixed. The host refuses a plugin on load for either failure,
// and the refusal names the plugin rather than the definition, so catch it here first.
// TestSchemasLoadThroughTheHost in protocol_test.go proves the same thing through the real host.
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
}
