package fake

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/infrena/infrena/pkg/provider"
	"github.com/infrena/infrena/pkg/schema"
	"github.com/infrena/infrena/pkg/value"
)

// Version is reported in the handshake and checked against a project's `plugins:`
// constraint. It is "0.0.0-dev" in every build a release did not stamp: a checkout
// build claiming to be a release is how a bug report turns into an afternoon, and a
// default equal to plugin.yaml's version would let a broken -ldflags path pass the
// release check. scripts/build-release stamps it:
// -ldflags "-X github.com/infrena/infrena-provider-fake/internal/fake.Version=1.2.3".
var Version = "0.0.0-dev"

// Plugin is the fake provider before configuration: its schemas, and how to build one
// configured instance of itself.
type Plugin struct{}

// NewPlugin returns the fake provider's plugin.
func NewPlugin() *Plugin { return &Plugin{} }

var _ provider.Plugin = (*Plugin)(nil)

// Name is the plugin's name, which `plugin:` names and every type is prefixed with.
func (pl *Plugin) Name() string { return PluginName }

// Version reports this build's version.
func (pl *Plugin) Version() string { return Version }

// Definitions are the same for every instance and need no configuration.
func (pl *Plugin) Definitions() []*schema.ResourceDefinition { return definitions() }

// cloudKey is the fake provider's only configuration: which JSON file is this instance's world.
const cloudKey = "cloud"

// New builds one instance.
//
// `cloud:` stands in for an account, so two instances naming no file get a file EACH,
// named after the instance — except the implicit instance, which keeps the path every
// existing project already has.
func (pl *Plugin) New(cfg provider.Config) (provider.Provider, error) {
	if err := rejectUnknownKeys(cfg.Values); err != nil {
		return nil, err
	}
	path := defaultCloudPath(cfg.Instance)
	if v, declared := cfg.Value(cloudKey); declared {
		text, ok := v.AsString()
		if !ok {
			return nil, fmt.Errorf("`cloud` must be a path to a JSON file, got %s", v.Kind)
		}
		if text == "" {
			return nil, fmt.Errorf("`cloud` is empty: give the path to the JSON file holding "+
				"this instance's fake infrastructure, or omit the key to get %s", defaultCloudPath(cfg.Instance))
		}
		path = text
	}
	// Relative to the PROJECT, which the host supplies — not to this process's working
	// directory, which is inherited from infrena and is not where the project is.
	if !filepath.IsAbs(path) {
		path = filepath.Join(cfg.ProjectDir, path)
	}
	return New(path), nil
}

// defaultCloudPath is where an instance's world lives when it names no file.
func defaultCloudPath(instance string) string {
	if instance == "" || instance == PluginName {
		return DefaultCloudPath
	}
	return filepath.Join(filepath.Dir(DefaultCloudPath), "fake-cloud-"+instance+".json")
}

// rejectUnknownKeys fails closed. A misspelled `clowd:` quietly ignored means an instance
// silently sharing another's account, and the first sign is a plan proposing to destroy
// resources somebody else owns.
func rejectUnknownKeys(config map[string]value.Value) error {
	var unknown []string
	for k := range config {
		if k != cloudKey {
			unknown = append(unknown, strconv.Quote(k))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown configuration %s; the fake provider accepts only `cloud`", strings.Join(unknown, ", "))
}
