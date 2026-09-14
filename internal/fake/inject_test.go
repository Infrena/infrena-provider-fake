package fake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/infrena/infrena/pkg/address"
	"github.com/infrena/infrena/pkg/provider"
	"github.com/infrena/infrena/pkg/resource"
	"github.com/infrena/infrena/pkg/value"
)

func TestInjectedFailureIsClassified(t *testing.T) {
	p, path := newTestProvider(t)
	c, _ := LoadCloud(path)
	c.Failures = []FailureRule{{Op: "create", Address: "db", Nth: 1, Retryability: RetrySafe, Message: "throttled"}}
	_ = c.Save(path)

	_, err := p.Create(context.Background(), desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err == nil {
		t.Fatal("expected the injected failure")
	}
	if got := p.ClassifyError(err); got != provider.SafeToRetry {
		t.Errorf("ClassifyError = %v, want SafeToRetry", got)
	}
}

func TestNthReadRuleSurvivesAcrossOperations(t *testing.T) {
	// Read is the only operation with no save on its success path, so this is
	// the only test that actually exercises begin()'s unconditional save. A
	// create-based version of this test passes either way, because Create
	// persists the advanced counter itself.
	p, path := newTestProvider(t)
	ctx := context.Background()
	st, err := p.Create(ctx, desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create's result never carries Address: the address is the host's bookkeeping, and
	// TestResultsCarryOnlyWhatThePluginOwns asserts the plugin leaves it unset. In
	// production the host attaches it from the state it persisted before Read is ever
	// called. A test that chains Create's output straight into Read must attach it
	// itself, standing in for that host step, or the "read"/"net" rule below can never
	// match.
	st.Address = address.Address{Name: "net"}

	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	c.Failures = []FailureRule{{Op: "read", Address: "net", Nth: 2, Message: "second read fails"}}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := p.Read(ctx, st); err != nil {
		t.Fatalf("first Read should succeed: %v", err)
	}
	if _, err := p.Read(ctx, st); err == nil {
		t.Fatal("second Read should fail: the rule's counter must survive the reload between operations")
	}
}

// TestNthFailureRuleSurvivesAcrossOperations is the regression guard for a
// rule that is easy to break: FailureRule's Seen/Fired bookkeeping must be
// exported and persisted, and begin() must save the cloud after every
// ShouldFail call, not only on failure. The provider reloads the cloud file on
// every operation — it must, to observe hand-edited drift — so if Seen is not
// written back after a non-firing check, it resets to zero on the next load
// and an Nth: 2 rule can never fire.
func TestNthFailureRuleSurvivesAcrossOperations(t *testing.T) {
	p, path := newTestProvider(t)
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	c.Failures = []FailureRule{{Op: "create", Address: "db", Nth: 2, Message: "boom"}}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	attrs := map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}

	if _, err := p.Create(context.Background(), desired("db", "fake.database", attrs)); err != nil {
		t.Fatalf("first Create must succeed (Nth: 2 has not fired yet), got: %v", err)
	}

	_, err = p.Create(context.Background(), desired("db", "fake.database", attrs))
	if err == nil {
		t.Fatal("second Create must fail: the rule's Seen counter must have persisted across the first call")
	}
	if got := p.ClassifyError(err); got != provider.NotSafeToRetry {
		t.Errorf("ClassifyError = %v, want NotSafeToRetry (rule set no retryability)", got)
	}
}

// TestFailureRuleReachesAllThreeClassifications drives ClassifyError from the
// cloud file, the way a person or infrena's own executor tests would.
//
// The rule once carried a `Retryable bool`, which maps onto exactly two of the
// three provider.Retryability constants. infrena's executor treats all three
// differently, so with a boolean a third of its retry behaviour could never be
// exercised — and the fake provider is the only thing that will ever produce
// these errors.
func TestFailureRuleReachesAllThreeClassifications(t *testing.T) {
	cases := []struct {
		name  string
		field string
		want  provider.Retryability
	}{
		{"absent defaults to not safe", "", provider.NotSafeToRetry},
		{"not_safe", `"retryability": "not_safe",`, provider.NotSafeToRetry},
		{"conditional", `"retryability": "conditional",`, provider.ConditionallyRetryable},
		{"safe", `"retryability": "safe",`, provider.SafeToRetry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fake-cloud.json")
			doc := `{
  "resources": {},
  "failures": [
    {"op": "create", "address": "db", "nth": 1, ` + tc.field + ` "message": "boom"}
  ]
}`
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatalf("write cloud: %v", err)
			}
			p := New(path)

			_, err := p.Create(context.Background(), desired("db", "fake.database", map[string]value.Value{
				"engine": value.String("postgres", value.SourceExplicit),
			}))
			if err == nil {
				t.Fatal("expected the injected failure")
			}
			if got := p.ClassifyError(err); got != tc.want {
				t.Errorf("ClassifyError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOperationsOverlapRatherThanSerialise(t *testing.T) {
	// The mutex exists to protect the cloud file, not to serialise simulated
	// latency. If it covers the delay, every concurrency test run against this
	// provider passes while proving nothing.
	p, path := newTestProvider(t)
	ctx := context.Background()

	const (
		resources = 4
		delayMS   = 150
	)

	states := make([]*resource.ResourceState, 0, resources)
	for i := range resources {
		st, err := p.Create(ctx, desired(fmt.Sprintf("net%d", i), "fake.network", map[string]value.Value{
			"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
		}))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		states = append(states, st)
	}

	// Introduce latency only now, so setup is not slowed.
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	c.LatencyMS = delayMS
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, resources)
	for i, st := range states {
		wg.Add(1)
		go func(i int, st *resource.ResourceState) {
			defer wg.Done()
			_, errs[i] = p.Read(ctx, st)
		}(i, st)
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Read %d: %v", i, err)
		}
	}

	// Serial execution takes at least resources*delay. Overlapping execution
	// takes roughly one delay. The midpoint is a generous threshold that is
	// not sensitive to scheduling noise.
	serial := time.Duration(resources) * delayMS * time.Millisecond
	if elapsed >= serial/2 {
		t.Errorf("%d concurrent reads with %dms latency took %v; serial would be ~%v. "+
			"The provider is serialising — the mutex is covering the delay, so no "+
			"concurrency test against this provider can fail.", resources, delayMS, elapsed, serial)
	}
}

// TestConcurrentCreatesDoNotLoseUpdates exercises the load-mutate-save cycle
// from several goroutines at once.
//
// begin() loads the cloud, advances failure bookkeeping and saves on every
// operation — the read path included — and each CRUD method then saves again.
// With no guard, concurrent callers interleave and lose each other's writes.
// infrena's refresh reads every resource in state concurrently, so concurrent
// callers are the normal case, not an edge.
func TestConcurrentCreatesDoNotLoseUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	p := New(path)

	const n = 24
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.Create(context.Background(), &resource.DesiredResource{
				Address: address.Address{Name: fmt.Sprintf("net%02d", i)},
				Type:    "fake.network",
				Attrs: map[string]value.Value{
					"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
				},
			})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	if len(c.Resources) != n {
		t.Errorf("cloud holds %d resources after %d concurrent creates; writes were lost", len(c.Resources), n)
	}
	if c.NextID != n {
		t.Errorf("NextID = %d after %d concurrent creates, want %d", c.NextID, n, n)
	}
}

// TestConcurrentReadsSeeWholeFiles pairs concurrent readers with concurrent
// writers. begin() saves on the read path too, so without atomicity a reader
// can observe a truncated file and report it as a provider failure.
func TestConcurrentReadsSeeWholeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	p := New(path)

	seed, err := p.Create(context.Background(), &resource.DesiredResource{
		Address: address.Address{Name: "net"},
		Type:    "fake.network",
		Attrs:   map[string]value.Value{"cidr": value.String("10.0.0.0/16", value.SourceExplicit)},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 32)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := p.Read(context.Background(), seed)
			if err != nil {
				errs[i] = err
				return
			}
			if st == nil {
				errs[i] = fmt.Errorf("concurrent Read reported the resource gone")
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Read %d: %v", i, err)
		}
	}
}

// TestLatencyIsApplied. Without it, the overlap test above passes against a provider that
// ignores latency_ms entirely — overlapping reads of zero duration are fast either way.
func TestLatencyIsApplied(t *testing.T) {
	p, path := newTestProvider(t)
	if err := (&Cloud{Resources: map[string]*CloudResource{}, LatencyMS: 120}).Save(path); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := p.Discover(context.Background(), provider.DiscoverRequest{}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 120*time.Millisecond {
		t.Errorf("Discover took %v with latency_ms 120", elapsed)
	}
}

// TestCancellationDuringLatencyMutatesNothing. Cancellation is honoured BEFORE the mutating
// call, never after it: a create that already happened must be reported, but one that has not
// started yet may be abandoned cleanly.
func TestCancellationDuringLatencyMutatesNothing(t *testing.T) {
	p, path := newTestProvider(t)
	if err := (&Cloud{Resources: map[string]*CloudResource{}, LatencyMS: 5000}).Save(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Create(ctx, desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Create under a cancelled context returned %v, want context.DeadlineExceeded", err)
	}
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources) != 0 {
		t.Errorf("a cancelled Create still created %d resource(s)", len(c.Resources))
	}
}

// TestACancelledContextMutatesNothingWithoutLatency guards the case TestCancellationDuringLatencyMutatesNothing
// cannot reach: with latency_ms 0, delay used to return nil without consulting ctx at all, so an
// already-cancelled Create would fall through to the mutation and create a resource nobody asked
// for — the same bug the latency case guards against, just with the delay's select statement never
// reached to catch it.
func TestACancelledContextMutatesNothingWithoutLatency(t *testing.T) {
	p, path := newTestProvider(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Create(ctx, desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create under an already-cancelled context returned %v, want context.Canceled", err)
	}
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources) != 0 {
		t.Errorf("a cancelled Create with no latency still created %d resource(s)", len(c.Resources))
	}
}

// TestTwoInstancesOnOneFileDoNotLoseUpdates. One plugin process serves every configured
// instance, so two Providers naming the same cloud file must share a lock.
//
// Replacing lockFor with a per-Provider mutex is what makes this test fail.
func TestTwoInstancesOnOneFileDoNotLoseUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-cloud.json")
	a, b := New(path), New(path)

	const n = 48
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		prov := a
		if i%2 == 1 {
			prov = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = prov.Create(context.Background(), &resource.DesiredResource{
				Address: address.Address{Name: fmt.Sprintf("net%02d", i)},
				Type:    "fake.network",
				Attrs:   map[string]value.Value{"cidr": value.String("10.0.0.0/16", value.SourceExplicit)},
			})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources) != n {
		t.Errorf("cloud holds %d resources after %d creates across two instances; writes were lost", len(c.Resources), n)
	}
}
