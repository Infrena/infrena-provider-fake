package fake

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/infrata/infrata/pkg/provider"
	"github.com/infrata/infrata/pkg/resource"
	"github.com/infrata/infrata/pkg/schema"
)

// PluginName is this plugin's name: the binary's suffix, what `plugin:` names, and the
// prefix of every type it serves. The host refuses a mismatch in any of the three.
const PluginName = "fake"

// Provider is one configured instance of the fake provider, backed by one cloud file.
type Provider struct {
	cloudPath string

	// mu guards the load-mutate-save cycle on the cloud file. Every operation, Read
	// included, rewrites the file, so without it concurrent callers lose each other's
	// writes. Shared by every Provider naming the same file — see lockFor.
	mu *sync.Mutex
}

// cloudLocks holds one mutex per absolute cloud path.
//
// PER FILE, not per Provider: one plugin process serves every configured instance, and two
// instances whose `cloud:` names the same file would otherwise each hold their own lock and
// interleave writes. Paths reaching one file through a symlink are not unified.
var cloudLocks sync.Map

func lockFor(path string) *sync.Mutex {
	key, err := filepath.Abs(path)
	if err != nil {
		key = path
	}
	m, _ := cloudLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// New returns a provider backed by the cloud file at cloudPath.
func New(cloudPath string) *Provider {
	return &Provider{cloudPath: cloudPath, mu: lockFor(cloudPath)}
}

var _ provider.Provider = (*Provider)(nil)

// Name returns the plugin name.
func (p *Provider) Name() string { return PluginName }

// Definitions returns the resource types this provider serves.
func (p *Provider) Definitions() []*schema.ResourceDefinition { return definitions() }

// ClassifyError says whether a failed operation may be retried. Nothing this provider
// returns yet is known to be safe, so everything is NotSafeToRetry.
func (p *Provider) ClassifyError(err error) provider.Retryability { return provider.NotSafeToRetry }

// begin loads the cloud for one operation. Callers hold p.mu.
func (p *Provider) begin(op, addr string) (*Cloud, error) {
	return LoadCloud(p.cloudPath)
}

// Create creates a resource in the fake cloud and assigns it a provider ID.
func (p *Provider) Create(ctx context.Context, d *resource.DesiredResource) (*resource.ResourceState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	addr := d.Address.String()
	c, err := p.begin("create", addr)
	if err != nil {
		return nil, err
	}
	id := c.AllocateID(idPrefix(d.Type))
	attrs := make(map[string]any, len(d.Attrs))
	for name, v := range d.Attrs {
		attrs[name] = toRaw(v)
	}
	for name, v := range computedFor(d.Type, id) {
		attrs[name] = v
	}
	c.Resources[id] = &CloudResource{Type: d.Type, Address: addr, Attributes: attrs}
	if err := c.Save(p.cloudPath); err != nil {
		return nil, err
	}
	return stateOf(d.Type, id, attrs), nil
}

// Read reports a resource as the fake cloud holds it now, or (nil, nil) if it is gone —
// which is how a hand-deleted resource shows up as drift.
func (p *Provider) Read(ctx context.Context, current *resource.ResourceState) (*resource.ResourceState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, err := p.begin("read", current.Address.String())
	if err != nil {
		return nil, err
	}
	obj, ok := c.Resources[current.ProviderID]
	if !ok {
		return nil, nil
	}
	return stateOf(obj.Type, current.ProviderID, obj.Attributes), nil
}

// Update makes a resource's attributes match the desired state, keeping its ID.
//
// Attributes the desired state omits are REMOVED, except computed ones, which the desired
// state never carries. Merging instead leaves a dropped attribute in the cloud, and every
// later plan proposes removing it again.
func (p *Provider) Update(ctx context.Context, current *resource.ResourceState, d *resource.DesiredResource) (*resource.ResourceState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, err := p.begin("update", d.Address.String())
	if err != nil {
		return nil, err
	}
	obj, ok := c.Resources[current.ProviderID]
	if !ok {
		return nil, fmt.Errorf("cannot update %s: %s no longer exists in %s", d.Address, current.ProviderID, p.cloudPath)
	}
	def := definitionOf(obj.Type)
	for name := range obj.Attributes {
		if _, wanted := d.Attrs[name]; wanted {
			continue
		}
		if def != nil {
			if a, ok := def.Attribute(name); ok && a.Computed {
				continue
			}
		}
		delete(obj.Attributes, name)
	}
	for name, v := range d.Attrs {
		obj.Attributes[name] = toRaw(v)
	}
	if err := c.Save(p.cloudPath); err != nil {
		return nil, err
	}
	return stateOf(obj.Type, current.ProviderID, obj.Attributes), nil
}

// Delete removes a resource. Deleting one that is already gone succeeds.
func (p *Provider) Delete(ctx context.Context, current *resource.ResourceState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, err := p.begin("delete", current.Address.String())
	if err != nil {
		return err
	}
	delete(c.Resources, current.ProviderID)
	return c.Save(p.cloudPath)
}

// Discover reports every resource the cloud file holds, INCLUDING ones this project never
// created — a CloudResource written by hand has no `address`, and discovery does not care.
// Infrastructure that predates the tool is the only reason discovery exists.
//
// req.Types filters HERE rather than in the caller: a real provider answers one type with
// one API call, and the fake must not model a cheaper contract than a real one.
//
// Sorted by provider ID, because the listing is printed and diffed and Go's map order is not.
func (p *Provider) Discover(ctx context.Context, req provider.DiscoverRequest) ([]provider.DiscoveredResource, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, err := p.begin("discover", "")
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(req.Types))
	for _, t := range req.Types {
		wanted[t] = true
	}
	out := make([]provider.DiscoveredResource, 0, len(c.Resources))
	for id, obj := range c.Resources {
		if len(wanted) > 0 && !wanted[obj.Type] {
			continue
		}
		st := stateOf(obj.Type, id, obj.Attributes)
		out = append(out, provider.DiscoveredResource{Type: obj.Type, ProviderID: id, Attributes: st.Attributes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderID < out[j].ProviderID })
	return out, nil
}

// Import adopts one existing resource by its provider ID.
//
// The type is CHECKED against what the cloud holds, not trusted: `import fake.network db-9`
// naming a real database would otherwise write state claiming a database is a network, and
// the next plan would propose replacing real infrastructure to settle a disagreement the tool
// invented. No address is assigned — naming is infrata's job.
func (p *Provider) Import(ctx context.Context, resourceType, id string) (*resource.ResourceState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, err := p.begin("import", id)
	if err != nil {
		return nil, err
	}
	obj, ok := c.Resources[id]
	if !ok {
		return nil, fmt.Errorf("fake provider: no resource with ID %q exists in %s", id, p.cloudPath)
	}
	if obj.Type != resourceType {
		return nil, fmt.Errorf("fake provider: %q is a %s, not a %s", id, obj.Type, resourceType)
	}
	return stateOf(obj.Type, id, obj.Attributes), nil
}

func computedFor(resourceType, id string) map[string]any {
	switch resourceType {
	case "fake.network":
		return map[string]any{"id": id}
	case "fake.database":
		return map[string]any{"endpoint": id + ".db.fake"}
	case "fake.application":
		return map[string]any{"url": "https://" + id + ".fake"}
	default:
		return nil
	}
}

func idPrefix(resourceType string) string {
	switch resourceType {
	case "fake.network":
		return "net"
	case "fake.database":
		return "db"
	case "fake.application":
		return "app"
	default:
		return strings.ReplaceAll(resourceType, ".", "-")
	}
}
