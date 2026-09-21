package fake

import (
	"os"
	"strings"
	"testing"
)

// TestReadmeQuotesTheTestedExample. The README's infrena.yml is the first thing anyone copies;
// the e2e suite runs that exact file, so the README must quote it byte for byte.
func TestReadmeQuotesTheTestedExample(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../e2e/testdata/basic/infrena.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), string(fixture)) {
		t.Error("README.md does not quote e2e/testdata/basic/infrena.yml verbatim")
	}
	for _, want := range []string{
		// The guide is asserted under its CURRENT filename: this list once pinned
		// an older name and so stayed green only while README carried a link to a
		// deleted file. A test that pins a broken state in place looks, to whoever
		// fixes the link, like proof they broke something.
		DefaultCloudPath, string(RetryNotSafe), string(RetryConditional), string(RetrySafe), "latency_ms",
		"docs/writing-a-provider.md",
		"infrena discover", "import dev fake.network.", "-tags e2e", "INFRENA_SRC", "plugin.yaml", "scripts/release-check", "0.0.0-dev",
	} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md never mentions %q", want)
		}
	}
}
