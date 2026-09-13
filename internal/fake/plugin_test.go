package fake

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrata/infrata/pkg/provider"
	"github.com/infrata/infrata/pkg/value"
)

// cloudPathOf builds an instance and reports which file it opens, which is the only
// thing this plugin's configuration decides.
func cloudPathOf(t *testing.T, dir, instance string, config map[string]value.Value) string {
	t.Helper()
	p, err := NewPlugin().New(provider.Config{Instance: instance, Values: config, ProjectDir: dir})
	if err != nil {
		t.Fatalf("New(%q): %v", instance, err)
	}
	return p.(*Provider).cloudPath
}

// TestAnExplicitCloudPathIsResolvedAgainstTheProjectDirectory. Against the PROJECT,
// not the process's working directory, which --chdir moves out from under us.
func TestAnExplicitCloudPathIsResolvedAgainstTheProjectDirectory(t *testing.T) {
	dir := t.TempDir()
	got := cloudPathOf(t, dir, "main", map[string]value.Value{
		"cloud": value.String("clouds/one.json", value.SourceVariable),
	})
	if want := filepath.Join(dir, "clouds", "one.json"); got != want {
		t.Errorf("cloudPath = %q, want %q", got, want)
	}
}

// TestTwoInstancesNamingNoCloudGetDifferentFiles.
//
// The default IS the behaviour: `cloud:` is this provider's stand-in for an account,
// so two instances sharing one file would be the same mistake as two AWS instances
// sharing one set of credentials. A sabotage collapsing this to one path broke nothing
// in the suite when it was written, which is why it is asserted here as well as
// end to end.
func TestTwoInstancesNamingNoCloudGetDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	main := cloudPathOf(t, dir, "main", nil)
	acct2 := cloudPathOf(t, dir, "acct2", nil)
	if main == acct2 {
		t.Fatalf("both instances opened %q, so they are two names for one account", main)
	}
	for _, tc := range []struct{ instance, path string }{{"main", main}, {"acct2", acct2}} {
		if !strings.Contains(tc.path, tc.instance) {
			t.Errorf("%s opened %q, which does not identify it", tc.instance, tc.path)
		}
	}
}

// TestTheImplicitInstanceKeepsTheHistoricalPath. A project with no `providers:` block has one
// implicit instance, named after the PLUGIN (infrata internal/providers/prepare.go). providers/test
// special-cased "test"; renamed to fake, the implicit instance would otherwise open
// fake-cloud-fake.json — an empty cloud — and the first plan would propose recreating
// everything the project already has. "" is the same instance before a name is assigned.
func TestTheImplicitInstanceKeepsTheHistoricalPath(t *testing.T) {
	dir := t.TempDir()
	for _, instance := range []string{PluginName, ""} {
		if got, want := cloudPathOf(t, dir, instance, nil), filepath.Join(dir, DefaultCloudPath); got != want {
			t.Errorf("instance %q: cloudPath = %q, want the historical %q", instance, got, want)
		}
	}
}

// TestAnAbsoluteCloudPathIsUsedAsWritten. Joining it onto the project directory would
// quietly point the instance at a different, empty cloud.
func TestAnAbsoluteCloudPathIsUsedAsWritten(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "elsewhere.json")
	got := cloudPathOf(t, t.TempDir(), "main", map[string]value.Value{
		"cloud": value.String(abs, value.SourceExplicit),
	})
	if got != abs {
		t.Errorf("cloudPath = %q, want the absolute path as written, %q", got, abs)
	}
}

// TestAnUnknownConfigurationKeyIsRefused — fail closed. A misspelled `clowd:` quietly
// ignored means an instance silently sharing another's account, and the first sign of
// it is a plan proposing to destroy resources somebody else owns.
func TestAnUnknownConfigurationKeyIsRefused(t *testing.T) {
	_, err := NewPlugin().New(provider.Config{Instance: "main", Values: map[string]value.Value{
		"clowd": value.String("other.json", value.SourceExplicit),
	}, ProjectDir: t.TempDir()})
	if err == nil {
		t.Fatal("an unknown configuration key must be refused")
	}
	for _, want := range []string{"clowd", "cloud"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q — the reader needs the key they wrote and "+
				"the one they meant: %v", want, err)
		}
	}
}

// TestACloudPathOfTheWrongKindIsRefused, rather than silently formatted into one.
func TestACloudPathOfTheWrongKindIsRefused(t *testing.T) {
	_, err := NewPlugin().New(provider.Config{Instance: "main", Values: map[string]value.Value{
		"cloud": value.Int(7, value.SourceExplicit),
	}, ProjectDir: t.TempDir()})
	if err == nil {
		t.Fatal("`cloud: 7` must be refused")
	}
	if !strings.Contains(err.Error(), "cloud") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

// TestAnEmptyCloudPathIsRefused. filepath.Join with "" silently yields the project
// directory, so the instance would open a DIRECTORY and report an unhelpful I/O error
// much later.
func TestAnEmptyCloudPathIsRefused(t *testing.T) {
	_, err := NewPlugin().New(provider.Config{Instance: "main", Values: map[string]value.Value{
		"cloud": value.String("", value.SourceExplicit),
	}, ProjectDir: t.TempDir()})
	if err == nil {
		t.Fatal("an empty `cloud:` must be refused")
	}
	// And it says what omitting the key would have given, which is what the user
	// probably wanted.
	if !strings.Contains(err.Error(), "fake-cloud-main.json") {
		t.Errorf("the error does not name the default it would otherwise have used: %v", err)
	}
}

// TestPluginIdentity. The binary suffix, Name() and the type prefix must agree or the host
// refuses the plugin; Version is what a project's `plugins:` constraint is checked against.
func TestPluginIdentity(t *testing.T) {
	pl := NewPlugin()
	if pl.Name() != "fake" {
		t.Errorf("Name() = %q, want fake", pl.Name())
	}
	if pl.Version() == "" {
		t.Error("Version() is empty")
	}
}
