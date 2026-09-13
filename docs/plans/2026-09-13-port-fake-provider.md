# Port the fake provider to `infrata-plugin-fake` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port infrata's in-tree `providers/test` into this repository as the standalone plugin
binary `infrata-plugin-fake`, serving `fake.*`, with the README, AGENT.md and
`docs/writing-a-provider.md` that make it the reference for plugin authors.

**Architecture:** One Go module. `cmd/infrata-plugin-fake` is a one-line `pluginsdk.Main`. All logic
lives in `internal/fake`, split by responsibility: schemas, the cloud file, the plugin (configuration),
the provider (operations). Unit tests call `Provider` directly; protocol tests go through infrata's
public `pkg/plugintest`; a compliance suite behind `-tags e2e` drives a real `infrata` binary.

**Tech Stack:** Go 1.24.13, standard library, `github.com/infrata/infrata` (via `replace => ../ilan`).

**Spec:** this document. The design was approved in session on 2026-09-13; its decisions and the
verification behind them are recorded below rather than in a separate spec, because `CLAUDE.md`
requires the plan to carry both.

## Global Constraints

- Go `1.24.13`. Standard library plus `github.com/infrata/infrata` only — no other `require`.
- `go.mod` carries `replace github.com/infrata/infrata => ../ilan` (infrata is not fetchable).
- Plugin name `fake`; binary `infrata-plugin-fake`; every type prefixed `fake.`.
- Nothing ever writes to stdout. Logs, if any, go to stderr.
- `Create`/`Update` never return `(nil, nil)`.
- Do not reimplement host rules: no carry-forward, no timestamp stamping, no `WithSensitive`,
  no `WithSource`, no `Address`/`Provider` on returned state.
- Every test command uses `-count=1`. Every test gets a sabotage that COMPILES and changes
  behaviour, recorded in the task's commit message.
- Stage explicit paths only. Never `git add -A`, `git add .`, `git commit -am`.
- Commit messages explain why, and end with the session's `Co-Authored-By`/`Claude-Session` lines.
- infrata (`../ilan`) is NOT modified by this plan. New infrata defects go to the vault note's
  follow-ups (`projects/labs/infra-tool.md`).

---

## Decisions

| # | Decision | Why | Cost |
| --- | --- | --- | --- |
| D1 | Name the plugin `fake`, serving `fake.*` | This repository is what plugin authors copy; its lesson is that repo suffix, binary name, `Name()` and type prefix are one word. `test` also reads as "a test helper". | Breaking rename for existing `test.*` projects and state. Infrata's builtin `test` keeps serving them until it is deleted (infrata follow-up). No migration here. |
| D2 | The implicit instance (`fake` or `""`) keeps `.infra/fake-cloud.json` | The implicit instance is named after the plugin. `providers/test` special-cased `test`; without an equivalent for `fake` the implicit instance would open `fake-cloud-fake.json`, an empty cloud, and the first plan would propose recreating everything. | One special case, pinned by a test. |
| D3 | Port, delete, or change each behaviour per the ledger below | `internal/pluginhost/adapter.go` now enforces carry-forward, sensitivity, provenance, undeclared-attribute refusal and nil-from-create; `trust_test.go` covers each. A plugin duplicating them passes its tests when the host is broken. | Four existing unit tests are not ported (they assert deleted behaviour). |
| D4 | Fix `Update` to delete non-computed attributes the desired state omits | Reproduced against the builtin: remove `tags:`, plan shows `tags -> (absent)`, apply "succeeds", the cloud keeps `tags`, and every later plan repeats the update. Desired state never carries computed attributes, so a replace-all would drop `endpoint`; computed ones are kept. | A behaviour change from `providers/test`, approved in session. |
| D5 | One lock per absolute cloud path, not per `Provider` value | One process now serves every configured instance. Two instances naming the same `cloud:` file would each hold their own mutex and lose each other's writes. | A package-level `sync.Map`. Symlinked paths to one file are not unified (documented). |
| D6 | Protocol tests use `pkg/plugintest` in `go test ./...`; the real-binary suite is `-tags e2e` | `plugintest` runs infrata's own host over an in-memory pipe, so trust rules apply with no process. The e2e suite is slower and needs `../ilan` built; the user wants it run occasionally for compliance, not on every run. | e2e skips (loudly) when the infrata source is absent. |
| D7 | Committed `replace => ../ilan` | `go list -m github.com/infrata/infrata` fails (private). Spike: builds offline, zero `go.sum` lines, pulls only `pkg/*`. | Needs a sibling checkout named `ilan`; documented in README. |
| D8 | Bottom-up task order: cloud → provider → discover/import → injection → plugin+binary → e2e → docs | Each layer is testable on its own the moment it exists. `CLAUDE.md`'s suggested order starts with the binary, but `main` needs a `Plugin` whose `New` returns a working `Provider`, so schemas-first would ship a stub. `infrata explain` is verified in Task 5 instead. | The binary appears at Task 5, not Task 1. |

## Behaviour ledger (`providers/test` → `internal/fake`)

| Behaviour (source) | Verdict |
| --- | --- |
| 3 types, requirements, computed/sensitive/map/defaults (`definitions.go`) | Port, `test.` → `fake.` |
| Computed values `net-N`, `db-N.db.test`, `https://app-N.test` (`provider.go:381`) | Port as `db-N.db.fake`, `https://app-N.fake` |
| Cloud file: missing = empty, indented, atomic 0600 write (`cloud.go:101,136`) | Port |
| Failure rules: op/address/nth, persisted `seen`/`fired`, three retryabilities, unknown rejected (`cloud.go:37-126,173`) | Port |
| Latency outside the mutex, honouring ctx (`provider.go:79`) | Port |
| Discover sorted by ID with type filter; Import checks type (`provider.go:272,324`) | Port |
| `toRaw`/`fromRaw`, JSON null = unset (`provider.go:353,414,438`) | Port |
| Unknown config keys refused; `cloud:` wrong kind / empty refused (`plugin.go:52-106`) | Port |
| `Plugin.dir` from construction | Delete — `cfg.ProjectDir` is the only source over the protocol |
| `carryForward`, `CreatedAt`/`UpdatedAt` stamping (`provider.go:148,204,223`) | Delete (host: `adapter.go:304-318`) |
| `WithSensitive` from schema, `WithSource` (`provider.go:360-364`) | Delete (host: `adapter.go:352-358`) |
| `toState` setting `Address` (via `address.Parse`) and `Provider` (`provider.go:368-377`) | Delete (host re-attaches; ignores both on results) |
| `CloudPath()` exported for engine tests (`provider.go:479`) | Delete — no caller outside this module |
| `Update` merge that never removes an attribute (`provider.go:196`) | Change (D4) |
| Mutex per `Provider` (`provider.go:42`) | Change (D5) |

## Verification log

Every factual claim above was checked by reading code or running the CLI on 2026-09-13.

| Claim | How checked | Result |
| --- | --- | --- |
| `Plugin.New` takes `provider.Config`, not `(instance, config)` | `ilan/pkg/provider/provider.go:101` | **Contradicted AGENT.md §2** — fixed in Task 8 |
| `pluginhost.InProcess` is unreachable from this module | `ilan/internal/pluginhost/connect.go:34` | True when checked; **since resolved** by `pkg/plugintest` (`ilan` `b7f0de6`). AGENT.md §8 still names `InProcess` — Task 8 |
| Host re-attaches bookkeeping, forces sensitivity and provenance, refuses undeclared attributes | `adapter.go:282-362`; `trust_test.go:169-246` | True; each has a host test |
| SDK sends the full address string; failure rules matching on it still work, including in modules | `pluginsdk/serve.go:350`, `adapter.go:256`, `address.go:26` | True: `Address{Name: s}.String() == s` |
| `value.Value` keeps `int64` / `[]Value` / `map[string]Value` across the wire | `pkg/value/json.go:113-144` | True — `toRaw`/`fromRaw` port unchanged |
| A schema `Default: int64(10)` survives JSON | `pkg/schema/wire.go:16-70` | True |
| Implicit instance is named after the plugin | `internal/providers/prepare.go:152-167` | True — hence D2 |
| State-only commands pick the plugin from the type prefix | `internal/cli/context.go:236` | True — hence D1's cost |
| A binary on the search path beats the builtin | `internal/pluginhost/loader.go:89-103` | True |
| `github.com/infrata/infrata` is fetchable | `go list -m -versions` | **False** — hence D7 |
| `replace` build is offline and stdlib-only | spike module importing `pluginsdk` | True: 0 `go.sum` lines; deps are `pkg/{address,pluginproto,pluginsdk,provider,resource,schema,value}` |
| `plan` exit code | CLI run | **2 with changes, 0 clean** |
| `apply` / `destroy` exit code on success | CLI run | **2**, not 0 (assumed 0 — wrong) |
| An injected failure's exit code and output | CLI run | 1; output contains the rule's `message` |
| `refresh` reports drift | CLI run | **False as AGENT.md §10 words it** — prints `<addr>: refreshed`; the diff appears in the next `plan` |
| `plan` detects drift without `refresh` | CLI run after hand edit | True: `-/+ … (replacement forced by: cidr)` |
| `--output <file>` writes machine-readable plan | CLI run | True: JSON with `operations[].{address,type,provider,kind}` |
| `import <env> <type>.<id> --generate` | CLI run | True: writes `discovered/<plural>.yml` |
| `providers:` syntax | `internal/config/providers_test.go:84-145` | `- plugin: fake` / `name:` / `default: true`; resources select with `provider: <name>` |
| `Update` removes an omitted attribute | CLI run against builtin | **False** — permanent drift loop; hence D4 |
| `explain` works with no `infra.yml` | `infrata --chdir <empty dir> explain test.network` | True, exit 0 (builtin; plugin-dir loading by prefix checked in Task 5) |
| `pkg/plugintest` is importable from another module | Its own test cannot prove it — Go's `internal/` rule is per module (`ilan` `b7f0de6` message) | **Unproven until Task 5** — `protocol_test.go` compiling here is the check |
| The engine's in-tree `providers/test` has 36 tests | `grep '^func Test'` | True; 32 port (some moved between files), 4 are deleted (D3) |

## File structure

| Path | Responsibility |
| --- | --- |
| `go.mod` | module `github.com/infrata/infrata-provider-fake`, the `replace` |
| `.gitignore` | `/infrata-plugin-fake`, `/bin/` |
| `cmd/infrata-plugin-fake/main.go` | `pluginsdk.Main(fake.NewPlugin())`; `version` set by `-ldflags` |
| `internal/fake/cloud.go` | the cloud file: types, load, atomic save, failure rules, IDs |
| `internal/fake/definitions.go` | the three `fake.*` schemas |
| `internal/fake/provider.go` | `Provider`: CRUD, discover, import, classify, injection, locking |
| `internal/fake/values.go` | `toRaw` / `fromRaw` / `stateOf` — cloud JSON ↔ `value.Value` |
| `internal/fake/plugin.go` | `Plugin`: name, version, configuration, cloud path |
| `internal/fake/*_test.go` | unit tests per file; `protocol_test.go` via `pkg/plugintest` |
| `e2e/e2e_test.go` | `//go:build e2e` — builds both binaries, runs the CLI |
| `e2e/testdata/` | project fixtures the README also quotes |
| `README.md`, `AGENT.md`, `docs/writing-a-provider.md` | documentation deliverables |

---

### Task 1: Module and the cloud file

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/fake/cloud.go`, `internal/fake/cloud_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (used by every later task):
  - `const DefaultCloudPath = ".infra/fake-cloud.json"`
  - `type CloudResource struct { Type string; Address string; Attributes map[string]any }`
  - `type Retryability string` with `RetryNotSafe`, `RetryConditional`, `RetrySafe` and `(r Retryability) Classify() provider.Retryability`
  - `type FailureRule struct { Op, Address string; Nth int; Retryability Retryability; Message string; Seen int; Fired bool }`
  - `type Cloud struct { Resources map[string]*CloudResource; Failures []FailureRule; LatencyMS int; NextID int }`
  - `func LoadCloud(path string) (*Cloud, error)`, `func (c *Cloud) Save(path string) error`,
    `func (c *Cloud) ShouldFail(op, addr string) (*FailureRule, bool)`, `func (c *Cloud) Delay() time.Duration`,
    `func (c *Cloud) AllocateID(prefix string) string`

- [ ] **Step 1: Create the module**

`go.mod`:

```
module github.com/infrata/infrata-provider-fake

go 1.24.13

require github.com/infrata/infrata v0.0.0

// infrata is not yet published as a fetchable module, so this plugin builds against a
// sibling checkout at ../ilan. An outside plugin author has the same constraint today.
// Delete this line, and pin a real version above, once infrata is published.
replace github.com/infrata/infrata => ../ilan
```

`.gitignore`:

```
/infrata-plugin-fake
/bin/
```

- [ ] **Step 2: Write the failing tests**

`internal/fake/cloud_test.go` — port these six tests from `ilan/providers/test/cloud_test.go`
exactly (`TestLoadCloudMissingFileIsEmptyNotError`, `TestSaveThenLoadRoundTrips`, `TestSaveIsHumanEditable`,
`TestShouldFailMatchesNthOccurrence`, `TestShouldFailIgnoresOtherOpsAndAddresses`,
`TestSaveIsAtomicAndPrivate`), with `package fake` and `"test.database"` → `"fake.database"`. The two
concurrency tests in that file construct a `Provider` and move to Task 4. Add
`TestUnknownRetryabilityIsRejected` from `provider_test.go:498` here, since it tests `LoadCloud`:

```bash
cp ../ilan/providers/test/cloud_test.go internal/fake/cloud_test.go
```

Then edit it to: `package fake`; delete `TestConcurrentCreatesDoNotLoseUpdates` and
`TestConcurrentReadsSeeWholeFiles` and the now-unused imports (`context`, `fmt`, `sync`,
`pkg/address`, `pkg/resource`, `pkg/value`); replace `test.database` with `fake.database`; append:

```go
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
```

(add `"strings"` to the imports).

- [ ] **Step 3: Run to verify failure**

Run: `go test -count=1 ./internal/fake/`
Expected: FAIL to compile — `undefined: LoadCloud`, `undefined: Cloud`.

- [ ] **Step 4: Port `cloud.go`**

```bash
cp ../ilan/providers/test/cloud.go internal/fake/cloud.go
```

Edit exactly:
1. Package doc and clause become:
   ```go
   // Package fake implements infrata's fake provider: a provider whose "cloud" is a
   // hand-editable JSON file, so drift can be induced by a person or a test with equal ease.
   package fake
   ```
2. Delete the `// Spec §10's refresh reads every resource concurrently.` sentence from `Save`'s
   comment and replace `mirroring internal/state/local.go's Put` with `the same discipline
   infrata's own state file uses` — this repository must not point readers at infrata internals
   as if they could use them.
3. Nothing else changes: the types, `LoadCloud`, `Save`, `ShouldFail`, `Delay` and `AllocateID`
   port verbatim.

- [ ] **Step 5: Run to verify pass**

Run: `go mod tidy && go vet ./... && gofmt -l . && go test -count=1 ./internal/fake/`
Expected: `ok`; `gofmt -l` prints nothing; `go.mod` gains no `require` other than infrata.

- [ ] **Step 6: Sabotage**

One at a time, run the suite, confirm the named test fails, revert:
- `ShouldFail`: `if rule.Seen == nth` → `if rule.Seen >= nth` → `TestShouldFailMatchesNthOccurrence` (fires a third time).
- `Save`: `tmp.Chmod(0o600)` → `tmp.Chmod(0o644)` → `TestSaveIsAtomicAndPrivate`.
- `Save`: `json.MarshalIndent(c, "", "  ")` → `json.Marshal(c)` → `TestSaveIsHumanEditable`.
- `LoadCloud`: `if !rule.Retryability.valid()` → `if false && !rule.Retryability.valid()` → `TestUnknownRetryabilityIsRejected`.

- [ ] **Step 7: Commit**

```bash
git add go.mod .gitignore internal/fake/cloud.go internal/fake/cloud_test.go
git commit -m "fake: the cloud file, ported unchanged from providers/test

The file's shape is the fake provider's user interface: people edit it by hand to
simulate drift and inject failures, so it moves across byte-compatible.
Sabotage-verified: nth comparison, file mode, indentation, retryability validation." -- go.mod .gitignore internal/fake/cloud.go internal/fake/cloud_test.go
```

---

### Task 2: Schemas, value conversion, and Read/Create/Update/Delete

**Files:**
- Create: `internal/fake/definitions.go`, `internal/fake/values.go`, `internal/fake/provider.go`,
  `internal/fake/provider_test.go`, `internal/fake/definitions_test.go`

**Interfaces:**
- Consumes: Task 1's `Cloud`, `CloudResource`, `LoadCloud`, `Save`, `AllocateID`.
- Produces:
  - `const PluginName = "fake"`
  - `func definitions() []*schema.ResourceDefinition`, `func definitionOf(resourceType string) *schema.ResourceDefinition`
  - `func toRaw(v value.Value) any`, `func fromRaw(raw any) value.Value`,
    `func stateOf(resourceType, id string, attrs map[string]any) *resource.ResourceState`
  - `type Provider struct` (fields `cloudPath string`, `mu *sync.Mutex`), `func New(cloudPath string) *Provider`
  - `func (p *Provider) begin(op, addr string) (*Cloud, error)` — Task 4 adds injection to its body
  - test helpers in `provider_test.go`: `newTestProvider(t) (*Provider, string)`,
    `desired(name, resourceType string, attrs map[string]value.Value) *resource.DesiredResource`

- [ ] **Step 1: Write the failing tests**

`internal/fake/definitions_test.go`:

```go
package fake

import (
	"strings"
	"testing"
)

// TestEveryTypeIsValidAndPrefixed. The host refuses a plugin on load for either failure,
// and the refusal names the plugin rather than the definition, so catch it here first.
// Task 5's protocol test proves the same thing through the real host.
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
```

`internal/fake/provider_test.go`:

```bash
cp ../ilan/providers/test/provider_test.go internal/fake/provider_test.go
```

Then edit it:
1. `package fake`; every `"test.network"`, `"test.database"`, `"test.application"` → `fake.*`.
2. KEEP, unchanged apart from (1): `newTestProvider`, `desired`, `TestReadReflectsExternalMutation`,
   `TestReadReturnsNilWhenDeletedExternally`, `TestUpdateAndDelete`, `TestNullAttributeIsTreatedAsUnset`,
   `TestCompositeAttributesRoundTripAsPlainJSON`.
3. DELETE (they assert behaviour the host now owns — D3): `TestSensitiveAttributeIsMarkedOnRead`,
   `TestUpdatePreservesDependencies`, `TestReadPreservesCarriedFields`, `TestCreateLeavesDependenciesNil`,
   and the comment block about `TestDiscoverAndImportAreNotImplementedYet`.
4. MOVE OUT (later tasks re-add them): every failure/latency test (`TestInjectedFailureIsClassified`,
   `TestNthReadRuleSurvivesAcrossOperations`, `TestNthFailureRuleSurvivesAcrossOperations`,
   `TestFailureRuleReachesAllThreeClassifications`, `TestUnknownRetryabilityIsRejected` (already in
   Task 1), `TestOperationsOverlapRatherThanSerialise`) → Task 4; every Discover/Import test plus
   `writeCloud` and `preexisting` → Task 3.
5. REPLACE `TestCreateAssignsProviderIDAndComputedAttributes` — its `SourceProvider` assertion is the
   host's rule — with:

```go
func TestCreateAssignsProviderIDAndComputedAttributes(t *testing.T) {
	p, _ := newTestProvider(t)
	st, err := p.Create(context.Background(), desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.ProviderID != "db-1" {
		t.Errorf("ProviderID = %q, want db-1", st.ProviderID)
	}
	if got, _ := st.Attributes["endpoint"].AsString(); got != "db-1.db.fake" {
		t.Errorf("endpoint = %q, want db-1.db.fake", got)
	}
}
```

6. ADD:

```go
// TestUpdateRemovesAnAttributeTheConfigurationDropped. providers/test merged desired
// attributes and never deleted one: removing `tags:` planned `tags -> (absent)`, apply
// reported success, the cloud kept the tags, and every later plan proposed the same
// update forever. Computed attributes are never in the desired state, so they must survive.
func TestUpdateRemovesAnAttributeTheConfigurationDropped(t *testing.T) {
	p, path := newTestProvider(t)
	ctx := context.Background()
	st, err := p.Create(ctx, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
		"tags": value.Map(map[string]value.Value{
			"team": value.String("data", value.SourceExplicit),
		}, value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := p.Update(ctx, st, desired("db", "fake.database", map[string]value.Value{
		"engine": value.String("postgres", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := updated.Attributes["tags"]; ok {
		t.Error("Update returned tags, which the desired state no longer has")
	}
	if _, ok := updated.Attributes["endpoint"]; !ok {
		t.Error("Update dropped the computed endpoint, which desired state never carries")
	}

	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	if _, ok := c.Resources[st.ProviderID].Attributes["tags"]; ok {
		t.Error("the cloud file still holds tags: the next plan would propose removing them again")
	}
}

// TestResultsCarryOnlyWhatThePluginOwns. The host re-attaches address, instance,
// dependencies, lifecycle and timestamps, and ignores them on a result. A plugin that sets
// them anyway is reimplementing a host rule, and its tests would pass with that rule broken.
func TestResultsCarryOnlyWhatThePluginOwns(t *testing.T) {
	p, _ := newTestProvider(t)
	st, err := p.Create(context.Background(), desired("net", "fake.network", map[string]value.Value{
		"cidr": value.String("10.0.0.0/16", value.SourceExplicit),
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.Address.String() != "" || st.Provider != "" || !st.CreatedAt.IsZero() || !st.UpdatedAt.IsZero() {
		t.Errorf("Create set host bookkeeping: address=%q provider=%q created=%v updated=%v",
			st.Address, st.Provider, st.CreatedAt, st.UpdatedAt)
	}
}
```

Fix the imports: run `go vet ./internal/fake/` and delete every import it reports as unused (after
the moves, expect `fmt`, `strings`, `sync`, `time` and `pkg/provider` to go; `bytes`, `encoding/json`,
`os`, `path/filepath` stay). Add nothing.

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 ./internal/fake/`
Expected: FAIL to compile — `undefined: definitions`, `undefined: New`, `undefined: PluginName`.

- [ ] **Step 3: Write `definitions.go`**

```bash
cp ../ilan/providers/test/definitions.go internal/fake/definitions.go
```

Edit: `package fake`; `test.network`/`test.database`/`test.application` → `fake.*` in `Type` and in
both `Requirements.Types`; the leading doc comment's type names likewise; delete the `PLAN.md §13`
comment on `size` and replace it with `// A default is a datum, not a function: a schema has to
survive a pipe.`. Append:

```go
// definitionOf returns the definition for a type, or nil for a type this plugin does not
// serve — which a hand-edited cloud file can contain.
func definitionOf(resourceType string) *schema.ResourceDefinition {
	for _, d := range definitions() {
		if d.Type == resourceType {
			return d
		}
	}
	return nil
}
```

- [ ] **Step 4: Write `values.go`**

Copy `toRaw` and `fromRaw` verbatim from `ilan/providers/test/provider.go:407-472` (including their
doc comments; in `toRaw`'s comment replace `a file spec §8.4 requires a human to be able to
hand-edit` with `a file a human is meant to hand-edit`). Add, above them:

```go
package fake

import (
	"fmt"

	"github.com/infrata/infrata/pkg/resource"
	"github.com/infrata/infrata/pkg/value"
)

// stateOf converts a cloud object into what a plugin reports: its type, its ID and its
// attributes, and nothing else.
//
// Deliberately NOT the address, the instance, dependencies, lifecycle or timestamps. The
// host never sends those and re-attaches them itself, and it forces sensitivity and
// provenance from the schema onto every value — so a plugin that also did any of it would
// be a plugin whose tests pass when the host is broken.
func stateOf(resourceType, id string, attrs map[string]any) *resource.ResourceState {
	out := make(map[string]value.Value, len(attrs))
	for name, raw := range attrs {
		// A JSON null means unset. Someone hand-editing the cloud file may null a value
		// out; turning it into the string "<nil>" would silently corrupt it.
		if raw == nil {
			continue
		}
		out[name] = fromRaw(raw)
	}
	return &resource.ResourceState{Type: resourceType, ProviderID: id, Attributes: out}
}
```

- [ ] **Step 5: Write `provider.go`**

```go
package fake

import (
	"context"
	"fmt"
	"path/filepath"
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

// Discover is implemented in Task 3.
func (p *Provider) Discover(ctx context.Context, req provider.DiscoverRequest) ([]provider.DiscoveredResource, error) {
	return nil, provider.ErrNotImplemented
}

// Import is implemented in Task 3.
func (p *Provider) Import(ctx context.Context, resourceType, id string) (*resource.ResourceState, error) {
	return nil, provider.ErrNotImplemented
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
```

- [ ] **Step 6: Run to verify pass**

Run: `go vet ./... && gofmt -l . && go test -count=1 ./internal/fake/`
Expected: `ok`, no `gofmt` output.

- [ ] **Step 7: Sabotage**

One at a time; confirm the named test fails; revert:
- `Update`: delete the whole `for name := range obj.Attributes { … }` removal loop → `TestUpdateRemovesAnAttributeTheConfigurationDropped` (tags remain).
- `Update`: change `ok && a.Computed` to `ok && false` → same test (endpoint dropped).
- `Read`: `return nil, nil` → `return stateOf(current.Type, current.ProviderID, nil), nil` → `TestReadReturnsNilWhenDeletedExternally`.
- `stateOf`: remove the `if raw == nil { continue }` → `TestNullAttributeIsTreatedAsUnset`.
- `toRaw`: make `case value.KindMap:` return `v.Raw` → `TestCompositeAttributesRoundTripAsPlainJSON`.
- `stateOf`: add `Provider: PluginName,` to the returned struct → `TestResultsCarryOnlyWhatThePluginOwns`.
- `definitions.go`: `"fake.network"` Type → `"test.network"` → `TestEveryTypeIsValidAndPrefixed`.

- [ ] **Step 8: Commit**

```bash
git add internal/fake/definitions.go internal/fake/definitions_test.go internal/fake/values.go internal/fake/provider.go internal/fake/provider_test.go
git commit -m "fake: schemas and CRUD, without the host's jobs, and an update that removes

Carry-forward, timestamps, schema sensitivity and provenance are enforced by infrata's
host adapter now; doing them here too would hide a broken host. Update now deletes
attributes the configuration dropped (keeping computed ones): the old merge left them in
the cloud and every later plan proposed the same removal forever.
Sabotage-verified: removal loop, computed guard, absent read, null, composite, bookkeeping, prefix." -- internal/fake/definitions.go internal/fake/definitions_test.go internal/fake/values.go internal/fake/provider.go internal/fake/provider_test.go
```

---

### Task 3: Discover and Import

**Files:**
- Modify: `internal/fake/provider.go` (replace the two `ErrNotImplemented` stubs)
- Create: `internal/fake/discover_test.go`

**Interfaces:**
- Consumes: Task 2's `Provider`, `begin`, `stateOf`, `newTestProvider`.
- Produces: working `Discover` and `Import`; test helpers `writeCloud(t, path string, c *Cloud)` and
  `preexisting() *Cloud` in `discover_test.go` (Task 4 does not use them; Task 6 has its own fixtures).

- [ ] **Step 1: Write the failing tests**

Create `internal/fake/discover_test.go` by moving from `ilan/providers/test/provider_test.go:577-770`:
`writeCloud`, `preexisting`, `TestDiscoverFindsEveryResourceInTheCloud`, `TestDiscoverIsDeterministic`,
`TestDiscoverFiltersByType`, `TestDiscoverOnAnEmptyCloudFindsNothing`, `TestImportReadsARealResourceByID`,
`TestImportOfAnUnknownIDNamesTheID`, `TestImportOfTheWrongTypeIsRefused`. Header:

```go
package fake

import (
	"context"
	"strings"
	"testing"

	"github.com/infrata/infrata/pkg/provider"
)
```

Edits while moving: every `test.*` type → `fake.*`; in `TestDiscoverFindsEveryResourceInTheCloud`
DELETE the `SourceProvider` block and the `password … Sensitive` block, and in
`TestImportReadsARealResourceByID` DELETE the `SourceProvider` block and the `password … Sensitive`
block. Those are host rules (D3); Task 5's `TestADiscoveredSecretIsRedactedByTheHost` proves them
through the protocol. Drop the `value` import if nothing else uses it.

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 -run 'Discover|Import' ./internal/fake/`
Expected: FAIL — every test reports `not implemented`.

- [ ] **Step 3: Implement**

Replace the two stubs in `provider.go` with (add `"sort"` to the imports):

```go
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
```

- [ ] **Step 4: Run to verify pass**

Run: `go vet ./... && gofmt -l . && go test -count=1 ./internal/fake/`
Expected: `ok`.

- [ ] **Step 5: Sabotage**

- Delete the `sort.Slice` line → `TestDiscoverIsDeterministic` (fails within its 20 runs; if it ever passes, raise the fixture from 6 to 12 resources and record that).
- `if len(wanted) > 0 && !wanted[obj.Type]` → `if false` → `TestDiscoverFiltersByType`.
- `if obj.Type != resourceType` → `if false` → `TestImportOfTheWrongTypeIsRefused`.
- In the unknown-ID error drop `id` from the arguments (`%q` → a fixed `"that ID"`) → `TestImportOfAnUnknownIDNamesTheID`.

- [ ] **Step 6: Commit**

```bash
git add internal/fake/provider.go internal/fake/discover_test.go
git commit -m "fake: discover and import

Discovery reports resources nobody created through infrata, because that is the case it
exists for; import checks the type instead of trusting it, because trusting it lets a plan
propose replacing a real resource to fix a mismatch the tool invented.
Sabotage-verified: sort, type filter, import type check, ID in error." -- internal/fake/provider.go internal/fake/discover_test.go
```

---

### Task 4: Failure injection, latency, and locking

**Files:**
- Modify: `internal/fake/provider.go` (`ClassifyError`, `begin`, new `ErrInjected` and `delay`, a
  delay block at the top of all six operations)
- Create: `internal/fake/inject_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces: `type ErrInjected struct { Message string; Retryability Retryability }` with `Error() string`;
  `func (p *Provider) delay(ctx context.Context) error`. These are the fake's test surface for
  infrata's executor, and README (Task 7) documents them.

- [ ] **Step 1: Write the failing tests**

Create `internal/fake/inject_test.go`:

```go
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

	"github.com/infrata/infrata/pkg/address"
	"github.com/infrata/infrata/pkg/provider"
	"github.com/infrata/infrata/pkg/resource"
	"github.com/infrata/infrata/pkg/value"
)
```

Move into it, unchanged apart from `test.*` → `fake.*`, from `ilan/providers/test/provider_test.go`:
`TestInjectedFailureIsClassified` (132), `TestNthReadRuleSurvivesAcrossOperations` (160),
`TestNthFailureRuleSurvivesAcrossOperations` (240), `TestFailureRuleReachesAllThreeClassifications` (456),
`TestOperationsOverlapRatherThanSerialise` (514); and from `cloud_test.go`:
`TestConcurrentCreatesDoNotLoseUpdates` (93), `TestConcurrentReadsSeeWholeFiles` (136). Then add:

```go
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

// TestTwoInstancesOnOneFileDoNotLoseUpdates. One plugin process serves every configured
// instance, so two Providers naming the same cloud file must share a lock (D5).
//
// Passes on first run — lockFor arrived in Task 2. Its evidence is the sabotage in Step 5.
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
```

(`os` is used by the moved `TestFailureRuleReachesAllThreeClassifications`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 ./internal/fake/`
Expected: it compiles (the moved tests use only `ClassifyError`, `FailureRule` and `RetrySafe`, which
exist) and FAILS at runtime —
`TestInjectedFailureIsClassified: expected the injected failure`, the two `Nth…` tests, all four
`TestFailureRuleReachesAllThreeClassifications` subtests, `TestLatencyIsApplied`, and
`TestCancellationDuringLatencyMutatesNothing`. The concurrency tests and
`TestTwoInstancesOnOneFileDoNotLoseUpdates` pass.

- [ ] **Step 3: Implement**

In `provider.go` add `"errors"` and `"time"` to the imports, and add below `PluginName`:

```go
// ErrInjected is a failure a rule in the cloud file asked for. ClassifyError recognises
// it and answers with the rule's declared retryability.
type ErrInjected struct {
	Message      string
	Retryability Retryability
}

func (e *ErrInjected) Error() string { return e.Message }
```

Replace `ClassifyError` and `begin`:

```go
// ClassifyError answers an injected failure with the retryability its rule declared, and
// anything else with NotSafeToRetry — the right default when a second attempt could create
// a second resource.
func (p *Provider) ClassifyError(err error) provider.Retryability {
	var injected *ErrInjected
	if errors.As(err, &injected) {
		return injected.Retryability.Classify()
	}
	return provider.NotSafeToRetry
}

// begin loads the cloud and applies failure injection. Callers hold p.mu.
//
// The cloud is saved whether or not a rule fires: ShouldFail advances a matching rule's
// persisted counter, and saving only on failure would reset it on the next load, so an
// nth greater than 1 could never be reached.
func (p *Provider) begin(op, addr string) (*Cloud, error) {
	c, err := LoadCloud(p.cloudPath)
	if err != nil {
		return nil, err
	}
	rule, failing := c.ShouldFail(op, addr)
	if err := c.Save(p.cloudPath); err != nil {
		return nil, err
	}
	if failing {
		msg := rule.Message
		if msg == "" {
			msg = fmt.Sprintf("injected %s failure for %s", op, addr)
		}
		return nil, &ErrInjected{Message: msg, Retryability: rule.Retryability}
	}
	return c, nil
}

// delay applies the cloud file's simulated latency, returning early if ctx is cancelled.
//
// OUTSIDE the lock, deliberately: the lock protects the file, and holding it across a sleep
// would serialise every operation and quietly disarm every concurrency test run against
// this provider. And BEFORE the mutation, so cancellation abandons only work not yet done.
func (p *Provider) delay(ctx context.Context) error {
	c, err := LoadCloud(p.cloudPath)
	if err != nil {
		return err
	}
	d := c.Delay()
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

Insert as the FIRST statement of `Create`, `Read`, `Update` (return `nil, err`), `Delete` (return
`err`), `Discover` and `Import` (return `nil, err`) — before `p.mu.Lock()`:

```go
	if err := p.delay(ctx); err != nil {
		return nil, err
	}
```

- [ ] **Step 4: Run to verify pass**

Run: `go vet ./... && gofmt -l . && go test -count=1 -race ./internal/fake/`
Expected: `ok` (with `-race`: the lock work is the point of this task).

- [ ] **Step 5: Sabotage**

- `begin`: move the `c.Save` call inside `if failing { … }` → both `Nth…SurvivesAcrossOperations` tests.
- `ClassifyError`: `return injected.Retryability.Classify()` → `return provider.NotSafeToRetry` → `TestFailureRuleReachesAllThreeClassifications/safe` and `/conditional`, `TestInjectedFailureIsClassified`.
- `Read`: move the delay block below `defer p.mu.Unlock()` → `TestOperationsOverlapRatherThanSerialise`.
- `delay`: `case <-ctx.Done(): return ctx.Err()` → `case <-ctx.Done(): <-time.After(d); return nil` → `TestCancellationDuringLatencyMutatesNothing`.
- `delay`: `if d <= 0` → `if true` → `TestLatencyIsApplied`.
- `New`: `mu: lockFor(cloudPath)` → `mu: &sync.Mutex{}` → `TestTwoInstancesOnOneFileDoNotLoseUpdates` (and not `TestConcurrentCreatesDoNotLoseUpdates`, which uses one instance — which is why the new test exists). If it passes by luck, run it with `-count=5` and record the result.

- [ ] **Step 6: Commit**

```bash
git add internal/fake/provider.go internal/fake/inject_test.go
git commit -m "fake: failure and latency injection, and a lock per cloud file

Injected failures carry all three retryability classes, because the fake provider is the
only thing that can make infrata's executor face each one. Latency is applied outside the
lock (so concurrency tests stay meaningful) and before the mutation (so a cancellation
never abandons a create that happened). The lock is per file because one plugin process
now serves every instance.
Sabotage-verified: persisted counter, classification, lock scope, cancellation, latency, shared lock." -- internal/fake/provider.go internal/fake/inject_test.go
```

---

### Task 5: The plugin, the binary, and tests through the real host

**Files:**
- Create: `internal/fake/plugin.go`, `internal/fake/plugin_test.go`, `internal/fake/protocol_test.go`,
  `cmd/infrata-plugin-fake/main.go`

**Interfaces:**
- Consumes: Tasks 1–4; `github.com/infrata/infrata/pkg/plugintest` (`Open(ctx, provider.Plugin, dir) (*Host, error)`,
  `(*Host).Configure(provider.Config) (provider.Provider, error)`, `Definitions()`, `Version()`, `Close()`).
- Produces: `type Plugin struct{}`, `func NewPlugin() *Plugin`, `var Version = "0.1.0"`,
  `func defaultCloudPath(instance string) string`. Binary `infrata-plugin-fake`.

**D9 (a small fix, approved in session 2026-09-13):** `providers/test` joined `cloud:` onto the project directory
unconditionally, so `cloud: /tmp/x.json` silently became `<project>/tmp/x.json`. An absolute path is
now used as written. `TestAnAbsoluteCloudPathIsUsedAsWritten` covers it.

- [ ] **Step 1: Write the failing tests**

`internal/fake/plugin_test.go` — port `ilan/providers/test/plugin_test.go` with these changes:
`package fake`; `NewPlugin(dir).New(provider.Config{Instance: i, Values: v})` becomes
`NewPlugin().New(provider.Config{Instance: i, Values: v, ProjectDir: dir})` everywhere (including the
`cloudPathOf` helper); replace `TestTheImplicitInstanceKeepsTheHistoricalPath` with the version below;
add the two new tests.

```go
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

// TestAnAbsoluteCloudPathIsUsedAsWritten (D9). Joining it onto the project directory would
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
```

`internal/fake/protocol_test.go`:

```go
package fake

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/infrata/infrata/pkg/address"
	"github.com/infrata/infrata/pkg/plugintest"
	"github.com/infrata/infrata/pkg/provider"
	"github.com/infrata/infrata/pkg/resource"
	"github.com/infrata/infrata/pkg/value"
)

// openHost connects this plugin to infrata's own host over an in-memory pipe, so every call
// is encoded, decoded and passed through the trust rules exactly as it is from a subprocess.
func openHost(t *testing.T) (*plugintest.Host, string) {
	t.Helper()
	dir := t.TempDir()
	host, err := plugintest.Open(context.Background(), NewPlugin(), dir)
	if err != nil {
		t.Fatalf("the host refused this plugin's schemas: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host, dir
}

// TestSchemasLoadThroughTheHost. Open fails for a type outside the `fake.` prefix, a reserved
// attribute name, or a default of the wrong kind. The size default is checked explicitly: it
// is the one value in a schema that has to survive JSON as an integer.
func TestSchemasLoadThroughTheHost(t *testing.T) {
	host, _ := openHost(t)
	if got := host.Version(); got != Version {
		t.Errorf("handshake version = %q, want %q", got, Version)
	}
	var db bool
	for _, d := range host.Definitions() {
		if d.Type != "fake.database" {
			continue
		}
		db = true
		a, _ := d.Attribute("size")
		if n, ok := a.Default.(int64); !ok || n != 10 {
			t.Errorf("size default after the wire = %#v (%T), want int64(10)", a.Default, a.Default)
		}
	}
	if !db {
		t.Fatal("fake.database did not arrive")
	}
}

// TestACreateRoundTripsThroughTheHost, landing the implicit instance's cloud where the
// project already keeps it.
func TestACreateRoundTripsThroughTheHost(t *testing.T) {
	host, dir := openHost(t)
	prov, err := host.Configure(provider.Config{Instance: PluginName})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	ctx := context.Background()
	st, err := prov.Create(ctx, &resource.DesiredResource{
		Address: address.Address{Name: "net"},
		Type:    "fake.network",
		Attrs:   map[string]value.Value{"cidr": value.String("10.0.0.0/16", value.SourceExplicit)},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.Address.String() != "net" || st.Provider != PluginName {
		t.Errorf("host did not re-attach bookkeeping: address=%q provider=%q", st.Address, st.Provider)
	}
	got, err := prov.Read(ctx, st)
	if err != nil || got == nil {
		t.Fatalf("Read = %v, %v", got, err)
	}
	if id, _ := got.Attributes["id"].AsString(); id != st.ProviderID {
		t.Errorf("id = %q, want %q", id, st.ProviderID)
	}
	if _, err := LoadCloud(filepath.Join(dir, DefaultCloudPath)); err != nil {
		t.Errorf("the implicit instance's cloud is not at %s: %v", DefaultCloudPath, err)
	}
}

// TestADiscoveredSecretIsRedactedByTheHost. This plugin deliberately does NOT mark sensitive
// values (D3); the host forces it from the schema. This is the test that says so end to end.
func TestADiscoveredSecretIsRedactedByTheHost(t *testing.T) {
	host, dir := openHost(t)
	if err := (&Cloud{Resources: map[string]*CloudResource{
		"db-9": {Type: "fake.database", Attributes: map[string]any{"engine": "postgres", "password": "hunter2"}},
	}}).Save(filepath.Join(dir, DefaultCloudPath)); err != nil {
		t.Fatal(err)
	}
	prov, err := host.Configure(provider.Config{Instance: PluginName})
	if err != nil {
		t.Fatal(err)
	}
	found, err := prov.Discover(context.Background(), provider.DiscoverRequest{})
	if err != nil || len(found) != 1 {
		t.Fatalf("Discover = %v, %v", found, err)
	}
	if !found[0].Attributes["password"].Sensitive {
		t.Error("a discovered password reached the engine unmarked")
	}
}

// TestAnInjectedFailureKeepsItsClassificationAcrossThePipe. An error value cannot cross a
// pipe; the SDK classifies on this side and the class travels with the message.
func TestAnInjectedFailureKeepsItsClassificationAcrossThePipe(t *testing.T) {
	host, dir := openHost(t)
	if err := (&Cloud{
		Resources: map[string]*CloudResource{},
		Failures:  []FailureRule{{Op: "create", Address: "net", Nth: 1, Retryability: RetrySafe, Message: "throttled"}},
	}).Save(filepath.Join(dir, DefaultCloudPath)); err != nil {
		t.Fatal(err)
	}
	prov, err := host.Configure(provider.Config{Instance: PluginName})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prov.Create(context.Background(), &resource.DesiredResource{
		Address: address.Address{Name: "net"}, Type: "fake.network",
		Attrs: map[string]value.Value{"cidr": value.String("10.0.0.0/16", value.SourceExplicit)},
	})
	if err == nil {
		t.Fatal("expected the injected failure")
	}
	if got := prov.ClassifyError(err); got != provider.SafeToRetry {
		t.Errorf("classification after the pipe = %v, want SafeToRetry", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 ./internal/fake/`
Expected: FAIL to compile — `undefined: NewPlugin`, `undefined: Version`.

- [ ] **Step 3: Write `plugin.go`**

```go
package fake

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/infrata/infrata/pkg/provider"
	"github.com/infrata/infrata/pkg/schema"
	"github.com/infrata/infrata/pkg/value"
)

// Version is reported in the handshake and checked against a project's `plugins:`
// constraint. Releases set it: -ldflags "-X github.com/infrata/infrata-provider-fake/internal/fake.Version=1.2.3".
var Version = "0.1.0"

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
	// directory, which is inherited from infrata and is not where the project is.
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
```

- [ ] **Step 4: Write `main.go`**

```go
// Command infrata-plugin-fake is infrata's fake provider. Infrata runs it; you do not.
package main

import (
	"github.com/infrata/infrata-provider-fake/internal/fake"
	"github.com/infrata/infrata/pkg/pluginsdk"
)

func main() { pluginsdk.Main(fake.NewPlugin()) }
```

- [ ] **Step 5: Run to verify pass**

Run: `go vet ./... && gofmt -l . && go test -count=1 -race ./...`
Expected: `ok` for `internal/fake`; `cmd/infrata-plugin-fake` reports `[no test files]`.

- [ ] **Step 6: Verify the binary by hand**

```bash
go build -o bin/infrata-plugin-fake ./cmd/infrata-plugin-fake
./bin/infrata-plugin-fake; echo "exit=$?"
(cd ../ilan && go build -o "$OLDPWD/bin/infrata" ./cmd/infrata)
mkdir -p /tmp/fake-explain && ./bin/infrata --chdir /tmp/fake-explain --plugin-dir ./bin explain fake.database
```

Expected: the first command prints `fake is an infrata provider plugin: it is run by infrata, not
directly.` and `exit=2`; `explain` prints `fake.database`, `engine … (replaces on change)`,
`password … (sensitive)`, `size … (default: 10)`, `Computed: endpoint`, `Requires: fake.network`.
`explain` needs no `infra.yml` (verified against the builtin). What is NOT yet verified is that it
loads a plugin named only by the type's prefix from `--plugin-dir`; if it reports the plugin missing,
write `project: p` plus a `providers: [{plugin: fake}]` block to `/tmp/fake-explain/infra.yml`, and
record the behaviour in the verification log.

- [ ] **Step 7: Sabotage**

- `defaultCloudPath`: `instance == "" || instance == PluginName` → `instance == ""` → `TestTheImplicitInstanceKeepsTheHistoricalPath`.
- `New`: remove the `if !filepath.IsAbs(path)` guard (always join) → `TestAnAbsoluteCloudPathIsUsedAsWritten`.
- `rejectUnknownKeys`: `if len(unknown) == 0` → `if true` → `TestAnUnknownConfigurationKeyIsRefused`.
- `definitions.go`: `size` `Default: int64(10)` → `Default: "10"` (compiles; wrong kind) → `TestSchemasLoadThroughTheHost` fails at `Open`. (Not `Default: 10`: an `int` is accepted and arrives as `int64(10)` — `ilan/pkg/schema/attribute.go:70` — so it discriminates nothing.)
- `stateOf`: add `if name == "password" { continue }` → `TestADiscoveredSecretIsRedactedByTheHost` fails. Honest limit: this proves the test reads the value; the sabotage that proves the HOST forces sensitivity is removing `WithSensitive` in `ilan/internal/pluginhost/adapter.go`, which infrata's `trust_test.go` owns. Do not edit `../ilan` to run it.
- `ClassifyError`: always `provider.NotSafeToRetry` → `TestAnInjectedFailureKeepsItsClassificationAcrossThePipe`.

- [ ] **Step 8: Commit**

```bash
git add internal/fake/plugin.go internal/fake/plugin_test.go internal/fake/protocol_test.go cmd/infrata-plugin-fake/main.go
git commit -m "fake: the plugin binary, tested through infrata's own host

plugintest runs the real host over a pipe, so schema validation, redaction and error
classification are proven the way a subprocess experiences them rather than asserted about
the provider in isolation. The implicit instance keeps its historical cloud path through the
rename; an absolute cloud path is no longer rebased onto the project.
Sabotage-verified: implicit path, absolute path, unknown keys, default kind, host redaction, classification." -- internal/fake/plugin.go internal/fake/plugin_test.go internal/fake/protocol_test.go cmd/infrata-plugin-fake/main.go
```

---

### Task 6: The compliance suite against a real `infrata` (`-tags e2e`)

Not part of `go test ./...` (D6). Run occasionally, and always before a release:
`go test -tags e2e -count=1 ./e2e/`.

**Files:**
- Create: `e2e/e2e_test.go`, `e2e/testdata/basic/infra.yml`, `e2e/testdata/instances/infra.yml`

**Interfaces:**
- Consumes: the binary from Task 5; infrata's CLI as recorded in the verification log (exit codes
  2/0/1, `--plugin-dir`, `--auto-approve`, `--output`, `import <env> <type>.<id> --generate`).
- Produces: `e2e/testdata/basic/infra.yml`, which Task 7's README quotes verbatim and Task 7's
  `TestReadmeQuotesTheTestedExample` checks.

- [ ] **Step 1: Write the fixtures**

`e2e/testdata/basic/infra.yml`:

```yaml
project: demo

environments:
  dev: {}

resources:
  network:
    type: fake.network
    cidr: 10.0.0.0/16

  db:
    type: fake.database
    engine: postgres
    password: hunter2
    network: ${network.id}
    tags:
      team: data

  app:
    type: fake.application
    image: nginx:1.27
    database_url: ${db.endpoint}
```

`e2e/testdata/instances/infra.yml`:

```yaml
project: accounts

environments:
  dev: {}

providers:
  - plugin: fake
    name: main
    default: true
  - plugin: fake
    name: acct2

resources:
  shared:
    type: fake.network
    cidr: 10.0.0.0/16

  isolated:
    type: fake.network
    provider: acct2
    cidr: 10.1.0.0/16
```

- [ ] **Step 2: Write the suite**

`e2e/e2e_test.go`:

```go
//go:build e2e

// Package e2e runs a real infrata binary against a real infrata-plugin-fake binary.
//
// Not part of `go test ./...`: it builds infrata from source, so it is slow and needs a
// checkout. Run it with `go test -tags e2e -count=1 ./e2e/`. INFRATA_SRC points at the
// checkout; the default is the sibling ../ilan that go.mod's replace already assumes.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	infrataBin string // absolute path to the built infrata
	pluginDir  string // directory holding the built infrata-plugin-fake
	skipReason string // non-empty when the binaries could not be built
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "fake-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(tmp)
		src := os.Getenv("INFRATA_SRC")
		if src == "" {
			src = filepath.Join("..", "..", "ilan")
		}
		if _, err := os.Stat(filepath.Join(src, "cmd", "infrata")); err != nil {
			skipReason = fmt.Sprintf("no infrata checkout at %s (set INFRATA_SRC): %v", src, err)
			fmt.Fprintln(os.Stderr, "E2E SKIPPED: "+skipReason)
			return m.Run()
		}
		infrataBin = filepath.Join(tmp, "infrata")
		pluginDir = filepath.Join(tmp, "plugins")
		for _, b := range []struct{ dir, out, pkg string }{
			{src, infrataBin, "./cmd/infrata"},
			{"..", filepath.Join(pluginDir, "infrata-plugin-fake"), "./cmd/infrata-plugin-fake"},
		} {
			cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
			cmd.Dir = b.dir
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b.pkg, err, out)
				return 1
			}
		}
		return m.Run()
	}()
	os.Exit(code)
}

// project copies a fixture into a fresh directory and returns it.
func project(t *testing.T, fixture string) string {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", fixture, "infra.yml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "infra.yml"), string(data))
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// infrata runs the CLI in dir and returns combined output and the exit code.
func infrata(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(infrataBin, append(args, "--plugin-dir", pluginDir)...)
	cmd.Dir = dir
	// Only the plugin directory above may supply plugins: nothing from the developer's machine.
	cmd.Env = append(os.Environ(), "INFRATA_PLUGIN_PATH=", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running infrata %v: %v", args, err)
	}
	return string(out), code
}

// expect runs a command and fails unless the exit code and every wanted substring match.
func expect(t *testing.T, dir string, wantCode int, want []string, args ...string) string {
	t.Helper()
	out, code := infrata(t, dir, args...)
	if code != wantCode {
		t.Fatalf("infrata %s: exit %d, want %d\n%s", strings.Join(args, " "), code, wantCode, out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("infrata %s: output lacks %q\n%s", strings.Join(args, " "), w, out)
		}
	}
	return out
}

// planOps runs `plan dev --output` and returns address → kind for every proposed operation.
func planOps(t *testing.T, dir string) map[string]string {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "plan.json")
	infrata(t, dir, "plan", "dev", "--output", outPath)
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("plan wrote no --output file: %v", err)
	}
	var doc struct {
		Operations []struct{ Address, Kind string } `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("plan --output is not the expected JSON: %v\n%s", err, data)
	}
	ops := map[string]string{}
	for _, op := range doc.Operations {
		ops[op.Address] = op.Kind
	}
	return ops
}

// editCloud changes the cloud file the way a person does: as plain JSON, not through this
// module's own types, so a change to the file's shape cannot hide from the suite.
func editCloud(t *testing.T, path string, edit func(doc map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, _ := json.MarshalIndent(doc, "", "  ")
	writeFile(t, path, string(out))
}

func resourcesOf(doc map[string]any) map[string]any {
	r, _ := doc["resources"].(map[string]any)
	return r
}

// TestTheWorkflow is AGENT.md's checklist, run in order against one project.
func TestTheWorkflow(t *testing.T) {
	dir := project(t, "basic")
	cloud := filepath.Join(dir, ".infra", "fake-cloud.json")

	t.Run("explain", func(t *testing.T) {
		expect(t, dir, 0, []string{"fake.database", "(replaces on change)", "(sensitive)", "(default: 10)", "fake.network"},
			"explain", "fake.database")
	})
	t.Run("plan proposes three creates", func(t *testing.T) {
		expect(t, dir, 2, []string{"Plan: 3 to create", "<sensitive>"}, "plan", "dev")
	})
	t.Run("apply creates them", func(t *testing.T) {
		expect(t, dir, 2, []string{"Apply complete: 3 applied, 0 failed"}, "apply", "dev", "--auto-approve")
		if n := len(planOps(t, dir)); n != 0 {
			t.Fatalf("re-plan after apply proposes %d operations, want none", n)
		}
	})
	t.Run("a hand edit is drift", func(t *testing.T) {
		editCloud(t, cloud, func(doc map[string]any) {
			for _, r := range resourcesOf(doc) {
				obj := r.(map[string]any)
				if obj["type"] == "fake.database" {
					obj["attributes"].(map[string]any)["engine"] = "mysql"
				}
			}
		})
		if kind := planOps(t, dir)["db"]; kind != "replace" {
			t.Fatalf("after changing a ForceNew attribute by hand, db plans as %q, want replace", kind)
		}
		expect(t, dir, 2, []string{"0 failed"}, "apply", "dev", "--auto-approve")
		if n := len(planOps(t, dir)); n != 0 {
			t.Fatalf("drift was not repaired: %d operations remain", n)
		}
	})
	t.Run("removing an optional attribute converges", func(t *testing.T) {
		body, _ := os.ReadFile(filepath.Join(dir, "infra.yml"))
		writeFile(t, filepath.Join(dir, "infra.yml"), strings.Replace(string(body), "    tags:\n      team: data\n", "", 1))
		if kind := planOps(t, dir)["db"]; kind != "update" {
			t.Fatalf("dropping tags plans db as %q, want update", kind)
		}
		expect(t, dir, 2, []string{"0 failed"}, "apply", "dev", "--auto-approve")
		if ops := planOps(t, dir); len(ops) != 0 {
			t.Fatalf("plan after removing tags still proposes %v — the update did not remove them (D4)", ops)
		}
	})
	t.Run("an injected failure fails the apply, once", func(t *testing.T) {
		body, _ := os.ReadFile(filepath.Join(dir, "infra.yml"))
		writeFile(t, filepath.Join(dir, "infra.yml"), string(body)+"\n  extra:\n    type: fake.network\n    cidr: 10.2.0.0/16\n")
		editCloud(t, cloud, func(doc map[string]any) {
			doc["failures"] = []any{map[string]any{
				"op": "create", "address": "extra", "nth": 1, "message": "injected: quota exceeded",
			}}
		})
		expect(t, dir, 1, []string{"injected: quota exceeded", "1 failed"}, "apply", "dev", "--auto-approve")
		expect(t, dir, 2, []string{"Apply complete: 1 applied, 0 failed"}, "apply", "dev", "--auto-approve")
	})
	t.Run("removing a resource destroys it", func(t *testing.T) {
		body, _ := os.ReadFile(filepath.Join(dir, "infra.yml"))
		writeFile(t, filepath.Join(dir, "infra.yml"), strings.Replace(string(body), "\n  extra:\n    type: fake.network\n    cidr: 10.2.0.0/16\n", "", 1))
		expect(t, dir, 2, []string{"1 to destroy"}, "plan", "dev")
		expect(t, dir, 2, []string{"0 failed"}, "apply", "dev", "--auto-approve")
	})
	t.Run("discover and import adopt what infrata did not create", func(t *testing.T) {
		editCloud(t, cloud, func(doc map[string]any) {
			resourcesOf(doc)["net-77"] = map[string]any{
				"type": "fake.network", "attributes": map[string]any{"cidr": "172.16.0.0/12", "id": "net-77"},
			}
		})
		expect(t, dir, 0, []string{"fake.network", "net-77"}, "discover")
		expect(t, dir, 0, []string{"1 resource imported"}, "import", "dev", "fake.network.net-77", "--generate")
		if ops := planOps(t, dir); len(ops) != 0 {
			t.Fatalf("plan after import --generate proposes %v, want nothing", ops)
		}
	})
	t.Run("destroy empties the cloud", func(t *testing.T) {
		expect(t, dir, 2, []string{"0 failed"}, "destroy", "dev", "--auto-approve")
		data, _ := os.ReadFile(cloud)
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		if n := len(resourcesOf(doc)); n != 0 {
			t.Fatalf("cloud still holds %d resources after destroy", n)
		}
	})
}

// TestTwoInstancesKeepSeparateClouds. `cloud:` stands in for an account: two instances of one
// plugin, served by one process, must never write into each other's file.
func TestTwoInstancesKeepSeparateClouds(t *testing.T) {
	dir := project(t, "instances")
	expect(t, dir, 2, []string{"Apply complete: 2 applied, 0 failed"}, "apply", "dev", "--auto-approve")

	holds := func(name string) []string {
		data, err := os.ReadFile(filepath.Join(dir, ".infra", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		var addrs []string
		for _, r := range resourcesOf(doc) {
			addrs = append(addrs, fmt.Sprint(r.(map[string]any)["address"]))
		}
		return addrs
	}
	if got := holds("fake-cloud-main.json"); len(got) != 1 || got[0] != "shared" {
		t.Errorf("main's cloud holds %v, want [shared]", got)
	}
	if got := holds("fake-cloud-acct2.json"); len(got) != 1 || got[0] != "isolated" {
		t.Errorf("acct2's cloud holds %v, want [isolated]", got)
	}
}
```

- [ ] **Step 3: Run it**

Run: `go vet -tags e2e ./e2e/ && go test -tags e2e -count=1 -v ./e2e/`
Expected: both tests PASS. `go test -count=1 ./...` (no tag) still does not compile or run `e2e`.

Where an assertion fails because the CLI's wording or exit code differs from the verification log,
do NOT loosen it silently: run the command by hand, record the real behaviour in the verification
log, and change the assertion to it. Where the fake is wrong, fix the fake.

- [ ] **Step 4: Sabotage**

Each against the built binary (the suite rebuilds it):
- Revert D4 (delete the removal loop in `Update`) → `removing an optional attribute converges`.
- `defaultCloudPath`: return `DefaultCloudPath` for every instance → `TestTwoInstancesKeepSeparateClouds`.
- `begin`: never return the injected error → `an injected failure fails the apply, once`.
- `Import`: drop the type check AND return type `fake.database` → `discover and import…` (import or re-plan fails); record which.
- Rename `INFRATA_SRC` to a missing path → both tests SKIP with the `E2E SKIPPED:` line on stderr (not PASS silently).

- [ ] **Step 5: Commit**

```bash
git add e2e/e2e_test.go e2e/testdata/basic/infra.yml e2e/testdata/instances/infra.yml
git commit -m "e2e: prove the plugin against a real infrata binary, on demand

Unit and plugintest coverage cannot show that infrata launches the binary, finds it on the
search path, or that the whole plan/apply/drift/import/destroy loop converges. This does,
behind a build tag because it builds infrata from source.
Sabotage-verified: update removal, instance separation, failure injection, import type, skip path." -- e2e/e2e_test.go e2e/testdata/basic/infra.yml e2e/testdata/instances/infra.yml
```

---

### Task 7: README.md, with its example tied to the tested fixture

**Files:**
- Modify: `README.md` (rewrite)
- Create: `internal/fake/readme_test.go`

**Interfaces:**
- Consumes: `e2e/testdata/basic/infra.yml` (Task 6); `DefaultCloudPath`, `RetryNotSafe`/`RetryConditional`/`RetrySafe` (Task 1).
- Produces: nothing code depends on.

- [ ] **Step 1: Write the failing test**

`internal/fake/readme_test.go` — in the normal suite, so the README cannot drift from the tested example:

```go
package fake

import (
	"os"
	"strings"
	"testing"
)

// TestReadmeQuotesTheTestedExample. The README's infra.yml is the first thing anyone copies;
// the e2e suite runs that exact file, so the README must quote it byte for byte.
func TestReadmeQuotesTheTestedExample(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../e2e/testdata/basic/infra.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), string(fixture)) {
		t.Error("README.md does not quote e2e/testdata/basic/infra.yml verbatim")
	}
	for _, want := range []string{DefaultCloudPath, string(RetryNotSafe), string(RetryConditional), string(RetrySafe), "latency_ms", "AGENT.md"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md never mentions %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 -run TestReadme ./internal/fake/`
Expected: FAIL — the current README quotes no fixture.

- [ ] **Step 3: Capture real output**

```bash
go build -o bin/infrata-plugin-fake ./cmd/infrata-plugin-fake
(cd ../ilan && go build -o "$OLDPWD/bin/infrata" ./cmd/infrata)
D=$(mktemp -d) && cp e2e/testdata/basic/infra.yml "$D/"
./bin/infrata --chdir "$D" --plugin-dir "$PWD/bin" plan dev
./bin/infrata --chdir "$D" --plugin-dir "$PWD/bin" apply dev --auto-approve
./bin/infrata --chdir "$D" --plugin-dir "$PWD/bin" plan dev
cat "$D/.infra/fake-cloud.json"
```

Keep the output: Step 4 pastes it verbatim (trim only the `Applied:` detail if it exceeds 25 lines,
and say so in the README).

- [ ] **Step 4: Write README.md**

Sections, in this order, each with the stated content:

1. **Title and two sentences.** What it is for, including "no network, no credentials": the fake
   provider for infrata, whose cloud is a JSON file on disk, so infrata's whole engine — planning,
   applying, drift, import, failure handling — can be exercised with no network and no credentials.
2. **Build and install.** The sibling layout (`ilan/` next to this repo, because of the `replace`);
   `go build -o infrata-plugin-fake ./cmd/infrata-plugin-fake`; the search path, first match wins:
   `--plugin-dir` / `INFRATA_PLUGIN_PATH`, `<project>/.infra/plugins/`, `~/.local/share/infrata/plugins/`,
   `$PATH`; `--verbose` shows which was used. Running the binary by hand prints that it is run by
   infrata and exits 2 — that is expected.
3. **Try it.** The fixture, quoted exactly, in a ```` ```yaml ```` block; then the three commands
   `infrata plan dev`, `infrata apply dev --auto-approve`, `infrata plan dev`, each followed by its
   Step 3 output. Note the exit codes: 2 when plan has changes or apply applied, 0 when clean.
4. **The cloud file.** Its path (`.infra/fake-cloud.json` for a project with no `providers:` block;
   `.infra/fake-cloud-<instance>.json` for a named instance; `cloud:` overrides, relative to the
   project or absolute); the Step 3 file content; a table of top-level keys `resources`, `failures`,
   `latency_ms`, `next_id`. **Editing it by hand is supported, and is how you simulate drift** —
   show changing `engine` to `mysql` and the resulting `-/+ … (replacement forced by: engine)` plan
   line. A resource added by hand with no `address` is what `infrata discover` / `import` find.
5. **Resource types.** One table per type from `definitions.go`: attribute, kind, and flags —
   Required, Computed, Sensitive, ForceNew (shown as "replaces on change"), Default — plus each
   type's requirement and computed-value format (`net-N`, `db-N.db.fake`, `https://app-N.fake`).
6. **Injecting failures and latency.** A `failures` example:
   ```json
   "failures": [
     {"op": "create", "address": "extra", "nth": 1, "retryability": "conditional", "message": "injected: quota exceeded"}
   ]
   ```
   Fields: `op` (`create`, `read`, `update`, `delete`, `discover`, `import`), `address` (infrata's
   address; for `import` the provider ID; for `discover` empty), `nth` (fires on the nth matching
   call, once; default 1), `retryability` (`not_safe` default, `conditional`, `safe` — what each
   makes infrata's executor do), `message`. `seen`/`fired` are written back by the plugin; delete
   them to re-arm a rule. An unknown retryability is refused with an error naming it. Then
   `"latency_ms": 250` — applied to every operation, concurrently, and abandoned if infrata cancels
   before the operation starts.
7. **Several instances.** The `e2e/testdata/instances/infra.yml` example and which file each instance uses.
8. **Upgrading from infrata's built-in `test` provider.** Types are renamed `test.*` → `fake.*`
   (why, in one sentence); the implicit instance's cloud file keeps its path; state naming `test.*`
   is not migrated by this plugin and keeps working only while infrata still ships the builtin.
9. **Development.** `go test -count=1 ./...`; `go test -tags e2e -count=1 ./e2e/` (needs the
   infrata checkout, `INFRATA_SRC` to override), run before a release.
10. **Writing your own provider.** Point at `AGENT.md` (condensed reference, copy it into your repo)
    and `docs/writing-a-provider.md` (the long-form guide).
11. **Licence.** Keep `TBD.` — not this plan's decision.

- [ ] **Step 5: Run to verify pass, and try the README cold**

Run: `go test -count=1 ./...`
Expected: `ok`.
Then follow sections 2–4 literally in a fresh temp directory, copying commands from the rendered
README (not from this plan). Every command must succeed and print what the README shows.

- [ ] **Step 6: Sabotage**

- Change `cidr: 10.0.0.0/16` to `cidr: 10.0.0.0/8` in the README only → `TestReadmeQuotesTheTestedExample`.
- Delete the `latency_ms` paragraph → same test, `never mentions "latency_ms"`.

- [ ] **Step 7: Commit**

```bash
git add README.md internal/fake/readme_test.go
git commit -m "docs: a README whose example is the one the e2e suite runs

The README's infra.yml is the first thing anyone copies, so a test holds it byte-identical
to the fixture that the compliance suite applies; its output is pasted from a real run.
Sabotage-verified: quoted fixture, required topics." -- README.md internal/fake/readme_test.go
```

---

### Task 8: Keep AGENT.md true

Every change below corrects something this port proved wrong or awkward. No code; the check is
the grep in Step 2 plus re-reading each changed section against the code it describes.

**Files:**
- Modify: `AGENT.md`

- [ ] **Step 1: Make the corrections**

1. **§2 `provider.Plugin`.** Replace `New(instance string, config map[string]value.Value) (Provider, error)`
   with `New(cfg provider.Config) (Provider, error)`, and describe `Config`: `Instance` (the name the
   user gave it), `Values` (resolved configuration, nothing reserved), `ProjectDir` (for resolving a
   relative path), plus `cfg.Value(key)`. Say why it is a struct: a new field is additive for plugins
   compiled by other people; a new parameter is not.
2. **§1, new subsection "Depending on infrata".** Until `github.com/infrata/infrata` is published, a
   plugin needs `replace github.com/infrata/infrata => <path to a checkout>` in `go.mod`, and needs
   nothing else: the SDK and its dependencies are standard library only.
3. **§7 The project directory.** `cfg.ProjectDir`, not "New receives". Add: use an absolute path as
   written — joining it onto the project directory silently rebases it (the fake provider did).
4. **§8 Testing.** Replace the `pluginhost.InProcess` bullet with `pkg/plugintest`, and show it:
   ```go
   host, err := plugintest.Open(ctx, myplugin.New(), t.TempDir())
   if err != nil { t.Fatal(err) }            // schemas refused on load
   defer host.Close()
   prov, err := host.Configure(provider.Config{Instance: "main"})
   ```
   State what it gives: the host's own adapter, so schema validation, forced sensitivity, provenance,
   undeclared-attribute refusal and error classification all apply. Add a bullet: "Do not assert in
   a unit test that your Provider marks sensitivity or carries bookkeeping — it should not; assert
   the host does it, through `plugintest`." Point at `internal/fake/protocol_test.go` as the example.
5. **§8 "Test the binary once".** Point at `e2e/` here as the example, and say to keep it behind a
   build tag if it builds infrata.
6. **§10 checklist.** Replace `Mutating the cloud outside infrata and running refresh reports the drift`
   with `Mutating the cloud outside infrata makes the next plan propose the change (refresh records it
   into state; it does not print a diff)`. Add `Removing an optional attribute from configuration
   converges: apply, then plan shows no changes` and `An absolute path in configuration is used as
   written`.
7. **§2 table, `Update`.** Add to Notes: "make the resource match `desired` — including removing what
   `desired` no longer has; `desired` never contains computed attributes, so keep those."
8. **§1 "Depending on infrata" (from item 2).** Also: the module's `go` directive must be at least
   infrata's own (1.27 as of 2026-09-13), and Go's error for a too-low directive does not name the
   dependency that caused it.
9. **§9 Releasing — the manifest.** Every plugin repository ships `plugin.yaml` at its root, per infrata
   `PLAN.md` §31.2: `manifest: 1` (checked first), `name`, `version`, `protocol` (a list), `platforms`
   (`GOOS/GOARCH` per published build), `description`; optional `infrata` (a `pkg/semver` constraint;
   absent means unconstrained, never `">= 0.0.0"`) and `source`. It is read at the release TAG, never
   the default branch. Deliberately absent: checksums (`SHA256SUMS` is a release asset), asset names
   (the convention `infrata-plugin-<name>_<version>_<goos>_<goarch>.tar.gz`, `.zip` on Windows), and
   resource types (`name` implies them). Point at this repository's `plugin.yaml`.
10. **§9 Releasing — the release gate.** A release must fail unless the git tag, `plugin.yaml`'s
    `version` and the binary's reported version agree; a drift test is a weaker substitute someone can
    delete. `Version()` should report `0.0.0-dev` unless a release stamps it with `-ldflags -X`,
    because a default equal to the manifest's version lets a broken `-ldflags` path pass the gate.
    Point at `scripts/release-check`, `scripts/build-release` and `.github/workflows/release.yml`.
11. **§9 Releasing — constraints.** A project's `plugins:` constraint is checked against the version
    the handshake reports; an unversioned plugin reports `0.0.0` and cannot satisfy any constraint above
    it. The syntax is `pkg/semver`'s: `>= <= != == > < =`, comma is AND, a bare version pins exactly,
    `0.4` means `0.4.0`, pre-release ignored.
12. **§10 checklist.** Add: `plugin.yaml` present and its `name`/`version`/`protocol` agree with the
    code; the release refuses a tag, manifest and binary that disagree; `Version()` is `0.0.0-dev` in
    an unstamped build.

- [ ] **Step 2: Check**

Run: `grep -n "InProcess\|New(instance\|refresh reports" AGENT.md`
Expected: no output.
Then, for each of items 1, 4 and 6, open the code it describes (`../ilan/pkg/provider/provider.go`,
`../ilan/pkg/plugintest/plugintest.go`, the Task 6 suite's drift subtest) and confirm the sentence
matches. Record any mismatch in the verification log and fix AGENT.md, not the code.

- [ ] **Step 3: Commit**

```bash
git add AGENT.md
git commit -m "docs: AGENT.md corrected by building the plugin it describes

Porting the fake provider found four places the guide was wrong or unusable: the New
signature, a test harness no outside module could import (now pkg/plugintest), drift being
reported by plan rather than refresh, and an Update contract that let removed attributes
loop forever. A guide that the reference plugin cannot follow is not a guide." -- AGENT.md
```

---

### Task 9: `docs/writing-a-provider.md`

The long-form guide: for a competent Go developer who knows their cloud's API and nothing about
infrata. AGENT.md is the condensed reference; this explains. Every code excerpt is copied from this
repository or from `../ilan/pkg/*` and cites `path:line`, so none is invented.

**Files:**
- Create: `docs/writing-a-provider.md`

- [ ] **Step 1: Write it, in these sections**

1. **What a plugin is.** A process infrata launches; NDJSON over stdio; `pluginsdk.Main`; stdout is
   the protocol (and why a stray print surfaces as an unrelated parse error much later); stderr is the
   log, prefixed and shown under `--verbose`, with its tail quoted when a plugin crashes; the cookie.
2. **The two interfaces, method by method, and what the host does to each result.** A table plus prose:
   `Plugin.Name` (must match the binary suffix and prefix every type — refused otherwise),
   `Definitions` (validated on load: prefix, reserved `prevent_destroy`/`retain`, `module.`, default
   kind), `New(cfg)` (an error renders as a configuration error against the `providers:` entry), optional
   `Version`; `Provider.Read` (`(nil, nil)` = gone), `Create`/`Update` (`(nil, nil)` becomes "may exist
   untracked"), `Delete` (already-gone succeeds), `Discover` (unmanaged resources included; unknown
   types skipped by the host), `Import` (host names it; check the type), `ClassifyError` (called on the
   plugin's side, result travels with the error). For each: what is sent (type, provider ID, address,
   attributes — never bookkeeping) and what the host rebuilds (`adapter.go` `rebuild`/`check`).
3. **Modelling a resource type.** When an attribute is `Computed`, `ForceNew`, `Sensitive`, has a
   `Default`, and what each causes downstream — with real plan lines from the Task 7 capture:
   `(known after apply)`, `-/+ … (replacement forced by: engine)`, `<sensitive>`,
   `10 [default, from provider default]`. Why a default is a datum. The `Update` contract (D4): make
   the resource match `desired`, remove what it no longer has, keep computed attributes.
4. **`Requirements`.** What missing-dependency detection gives a user (infrata reports the missing
   resource with a suggested fix before any API call) and how the fake's
   `fake.database → fake.network` requirement shows in `explain` (`Requires:`).
5. **Errors and retries.** The three classes and what infrata's executor does with each —
   read `../ilan/internal/executor` for the exact retry behaviour of `SafeToRetry`,
   `ConditionallyRetryable` and `NotSafeToRetry` per operation (create, update, delete, read) and
   state it precisely, citing the file; if a class behaves the same for some operation, say so rather
   than implying a difference. Why `NotSafeToRetry` is the default. Why a host-side failure (crash,
   broken pipe) is always `NotSafeToRetry`. Writing error messages that say what to do.
6. **Cancellation.** `cancel` is a message; the host waits for the real answer; honour `ctx` before a
   mutating call and between paginated reads, never after the mutation happened. The fake's
   `delay` as the example.
7. **Testing without a cloud account.** The three layers, each with this repo's file as the example:
   direct `Provider` tests against a fake of the cloud (here, a JSON file; for a real cloud,
   `httptest.Server`); `pkg/plugintest` for the protocol and trust rules (`protocol_test.go`), including
   what NOT to assert in a unit test; one binary-level suite behind a build tag (`e2e/`). Sabotage:
   a test is not evidence until breaking the code makes it fail, and the sabotage must compile.
8. **Credentials.** Take them as the cloud's own tooling does (standard env vars and config files),
   accept `providers:` overrides (which may come from a variable and so differ per environment), refuse
   unknown keys, never log a credential or a `Sensitive` value — stderr lands in CI logs, and the
   redaction machinery cannot protect what a plugin prints itself.
9. **What the host enforces, so you don't.** The list from `PLAN.md` §31.1, each with a sentence on
   the failure it prevents, and the explicit instruction not to reimplement any of it.
10. **Versioning and releasing.** `Version()` and `-ldflags -X`; building per `GOOS`/`GOARCH`; naming
    the artefact `infrata-plugin-<name>`; a project's `plugins: {name: ">= 1.2.0, < 2.0.0"}` constraint
    (comparison operators on MAJOR.MINOR.PATCH, comma is AND; one version per plugin because instances
    share a process); the protocol version, not the Go types, is the compatibility contract.
11. **Depending on infrata today.** The `replace` directive, until the module is published, and the
    `go` directive, which must be at least infrata's own (1.27 as of 2026-09-13).
12. **The manifest, `plugin.yaml`.** What infrata `PLAN.md` §31.2 asks of every plugin repository and
    why its shape follows its purpose (read over the network, before any binary is downloaded, by
    infrata builds for years): each key and whether it is required; why it is read at a release TAG
    and never the default branch; why the format is versioned when infrata's configuration language
    is not; why `infrata` is optional (absent means unconstrained, not `">= 0.0.0"`); and what is
    deliberately left out (checksums, asset names, resource types) and where each lives instead. This
    repository's `plugin.yaml` is the worked example.
13. **The release gate.** Why a release, not a drift test, is what keeps tag, manifest and binary in
    agreement; why `Version()` reports `0.0.0-dev` until a release stamps it; how `scripts/release-check`
    reads the version from the binary's own handshake with no infrata involved; the archive naming
    convention `plugins install` constructs, and `SHA256SUMS`. Cite `scripts/` and
    `.github/workflows/release.yml`.

- [ ] **Step 2: Verify every claim**

Make a checklist of every factual sentence about infrata's behaviour (expect 30–50). For each, open
the cited file or run the command, and tick it. Any sentence that cannot be checked is removed or
reworded as a recommendation. Append the notable findings — especially any that differ from AGENT.md
— to this plan's verification log, and fix AGENT.md in the same commit if it is wrong.

- [ ] **Step 3: Commit**

```bash
git add docs/writing-a-provider.md docs/plans/2026-09-13-port-fake-provider.md
git commit -m "docs: the long-form guide to writing an infrata provider

AGENT.md tells an author what to do; this explains why, for someone who knows their cloud
and not infrata. Every claim about the host was checked against its code and every excerpt
cites the file it came from, because the guide is only worth its accuracy." -- docs/writing-a-provider.md docs/plans/2026-09-13-port-fake-provider.md
```

---

### Task 10: Close out

**Files:**
- Modify: `CLAUDE.md` ("Current state" and a vault pointer), this plan (verification log)
- Vault (not in git): `projects/labs/infra-tool.md`, `projects/labs/daily/<date>.md`

- [ ] **Step 1: Full verification**

Run, and paste the results into the final report:

```bash
gofmt -l . && go vet ./... && go vet -tags e2e ./e2e/
go test -count=1 -race ./...
go test -tags e2e -count=1 -v ./e2e/
```

Expected: no `gofmt` output; `ok`; both e2e tests PASS (not SKIP). Then walk AGENT.md §10's
checklist and tick each item against evidence from this plan's tasks.

- [ ] **Step 1b: Correct this plan against what execution proved**

Apply each correction from the SDD ledger (`.superpowers/sdd/2026-09-13-port-fake-provider/progress.md`,
lines beginning `Ruling:` and `Plan verification-log facts`) to this document:
- Task 1 Step 6: replace the `Seen == nth` → `Seen >= nth` sabotage (an equivalent mutant) with moving
  `rule.Seen++` below the comparison (R3).
- Task 4 Step 5: the `begin` save sabotage fails only `TestNthReadRuleSurvivesAcrossOperations`, because
  Create persists the counter itself (R6); the moved `TestNthReadRuleSurvivesAcrossOperations` sets
  `st.Address`, because Create no longer returns one (R5).
- Global Constraints: Go `1.27.0`, following infrata's floor (R7).
- Verification log, new rows: `explain` loads a plugin from the type prefix via `--plugin-dir` with no
  project file; `plan --output` lists every resource including `kind: "noop"`; the `infrata:` floor is
  enforced for release builds and exempts development builds; `plugins:` is enforced against the
  handshake version; the discovery defect and its fix in infrata `de33b4d`; the handshake is readable
  with the cookie and empty stdin; the SDK's hand-run message has a second line.
- Decisions table: add R8–R14 in one line each, and D10 for the manifest and release gate (user
  direction, infrata `PLAN.md` §31.2).

- [ ] **Step 2: Update CLAUDE.md**

`CLAUDE.md` was edited by the infrata session in `5ce4f13` (the contract table gained §31.2, §61,
`pkg/semver`, `pkg/plugintest`, and the Go 1.27 note): keep those edits; do not rewrite over them.
Below the title, add `> Project notes (source of truth): Obsidian Vault/projects/labs/infra-tool.md`.
Replace "Current state" with: what is built (the plugin, the three test layers, the docs); how to run
each test layer; that the e2e suite needs the `ilan` checkout; and a short "Known limits" list — no
`test.*` state migration, symlinked cloud paths are not unified, `replace` until infrata is published.
Shorten "How to plan this work" to a pointer at this plan, since it has been done.

- [ ] **Step 3: Update the vault**

In `projects/labs/infra-tool.md` § "The fake provider as its own plugin repo": change "(under way …)"
to the completion date; add what shipped and any new verification findings; tick nothing that is not
done. Add a dated line to the change history and one bullet to that day's `projects/labs/daily/`
note linking the section. Record any new infrata defect as a `- [ ] … #follow-up` checkbox there.

- [ ] **Step 4: Commit and hand off**

```bash
git add CLAUDE.md docs/plans/2026-09-13-port-fake-provider.md
git commit -m "docs: record that the fake provider is built, and how to check it

CLAUDE.md said nothing was built; future sessions would plan work that is done." -- CLAUDE.md docs/plans/2026-09-13-port-fake-provider.md
```

Then use superpowers:requesting-code-review for a whole-branch review, and
superpowers:finishing-a-development-branch to integrate.

---

### Task 11: `plugin.yaml`, and a release that refuses to publish a version disagreement

Added 2026-09-13 at the user's direction, against infrata `PLAN.md` §31.2 (the agreed manifest).
§31.2: "Its release workflow must assert that THREE things agree: the git tag, the manifest's
`version`, and the binary's `Version()`." Validating the manifest's own format waits on infrata
publishing a parser (user decision); this task does not parse YAML in Go.

**Files:**
- Create: `plugin.yaml`, `scripts/release-check`, `scripts/build-release`, `scripts/scripts_test.go`,
  `.github/workflows/release.yml`
- Modify: `internal/fake/plugin.go` (the `Version` default)

**Interfaces:**
- Consumes: `cmd/infrata-plugin-fake` (Task 5); `internal/fake.Version` as the `-ldflags -X` target;
  the SDK handshake line `{"protocol":1,"name":"fake","version":"<v>"}`, printed when the binary runs
  with `INFRATA_PLUGIN_COOKIE` set and empty stdin (verified: exit 0).
- Produces: `plugin.yaml` (Task 12 validates it); archives named
  `infrata-plugin-fake_<version>_<goos>_<goarch>.tar.gz` (`.zip` for windows) plus `SHA256SUMS`.

**R9:** `Version` defaults to `"0.0.0-dev"`, stamped only by a release, as infrata's own is (§61.1).
With a default equal to the manifest's version, an unstamped binary passes the three-way check, and a
broken `-ldflags` path can never be caught. That is what `TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion` proves.
**R10:** the `linux/arm` build (GOARM=7) is archived as `…_linux_arm.tar.gz`, following §31.2's
`<goos>_<goarch>` convention that `plugins install` constructs, not infrata's own `armv7` spelling.

- [ ] **Step 1: Write the failing tests**

`scripts/scripts_test.go`:

```go
// Package scripts tests the release scripts: they are what stands between a tag and a
// published release whose manifest, archive names or binary disagree.
package scripts

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// manifestVersion reads plugin.yaml's version, so these tests follow the manifest rather
// than hard-coding a release number that the next bump would silently falsify.
func manifestVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "version:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	t.Fatal("plugin.yaml has no top-level version: line")
	return ""
}

func run(t *testing.T, env []string, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReleaseCheckPassesWhenTagManifestAndBinaryAgree(t *testing.T) {
	v := manifestVersion(t)
	out, err := run(t, nil, "release-check", "v"+v)
	if err != nil {
		t.Fatalf("release-check v%s failed: %v\n%s", v, err, out)
	}
	if !strings.Contains(out, "all say "+v) {
		t.Errorf("release-check did not confirm the agreement:\n%s", out)
	}
}

// TestReleaseCheckRefusesATagTheManifestDoesNotName. Judging a release by a manifest that
// describes a different version is the mistake §31.2 exists to prevent.
func TestReleaseCheckRefusesATagTheManifestDoesNotName(t *testing.T) {
	v := manifestVersion(t)
	out, err := run(t, nil, "release-check", "v99.0.0")
	if err == nil {
		t.Fatalf("release-check accepted v99.0.0 against a manifest saying %s:\n%s", v, out)
	}
	for _, want := range []string{"99.0.0", v} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
}

// TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion. `go build -X` on a symbol that
// does not exist is silently ignored, so a renamed Version variable would ship every
// archive reporting 0.0.0-dev. This simulates exactly that drift.
func TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion(t *testing.T) {
	v := manifestVersion(t)
	out, err := run(t,
		[]string{"FAKE_VERSION_SYMBOL=github.com/infrata/infrata-provider-fake/internal/fake.NoSuchVariable"},
		"release-check", "v"+v)
	if err == nil {
		t.Fatalf("release-check passed with a binary whose version was never stamped:\n%s", out)
	}
	if !strings.Contains(out, "0.0.0-dev") {
		t.Errorf("the refusal does not say what the binary reported:\n%s", out)
	}
}

// TestBuildReleaseNamesArchivesByTheInstallConvention. `infrata plugins install` constructs
// the download name rather than reading it (§31.2), so a wrong name is an uninstallable release.
func TestBuildReleaseNamesArchivesByTheInstallConvention(t *testing.T) {
	v := manifestVersion(t)
	dist := t.TempDir()
	if out, err := run(t, []string{"PLATFORMS=linux/amd64 windows/amd64"}, "build-release", v, dist); err != nil {
		t.Fatalf("build-release failed: %v\n%s", err, out)
	}
	stem := "infrata-plugin-fake_" + v + "_linux_amd64"
	for _, name := range []string{stem + ".tar.gz", "infrata-plugin-fake_" + v + "_windows_amd64.zip"} {
		if _, err := os.Stat(filepath.Join(dist, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}

	f, err := os.Open(filepath.Join(dist, stem+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = true
	}
	for _, want := range []string{stem + "/infrata-plugin-fake", stem + "/plugin.yaml", stem + "/README.md"} {
		if !got[want] {
			t.Errorf("%s.tar.gz does not contain %s; it holds %v", stem, want, got)
		}
	}
}
```

`plugin.yaml` (repo root). The `version` is the NEXT release, since this file on the default branch
describes unreleased code. `infrata` is omitted: no infrata release exists to name, and §31.2 makes
absence the honest form of "unconstrained".

```yaml
# plugin.yaml: what this plugin is, and what it works with. infrata PLAN.md §31.2.
# Read at a release TAG, never at the default branch, which describes unreleased code.
manifest: 1
name: fake
version: 0.1.0
protocol: [1]
platforms: [linux/amd64, linux/arm64, linux/arm, linux/386, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64]
description: A fake provider for testing infrata without a cloud account.
source: https://github.com/infrata/infrata-provider-fake
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 ./scripts/`
Expected: FAIL — `bash: release-check: No such file or directory` (and the same for `build-release`).

- [ ] **Step 3: Write `scripts/release-check`** (mode 0755)

```bash
#!/usr/bin/env bash
# release-check TAG: refuse to release unless the git tag, plugin.yaml's version and the version
# the built binary reports all agree (infrata PLAN.md §31.2). The manifest is authoritative and
# the binary secondary; this check is what blocks a release where they disagree.
#
# FAKE_VERSION_SYMBOL overrides the -ldflags -X target. It exists so a test can simulate the
# variable being renamed, which `go build -X` otherwise ignores in silence.
set -euo pipefail

tag="${1:?usage: scripts/release-check vMAJOR.MINOR.PATCH}"
version="${tag#v}"
if [[ "$tag" != v* || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release-check: tag '$tag' is not vMAJOR.MINOR.PATCH" >&2
  exit 1
fi

cd "$(dirname "$0")/.."

manifest_version="$(sed -n 's/^version:[[:space:]]*//p' plugin.yaml | tr -d "\"' ")"
if [[ -z "$manifest_version" ]]; then
  echo "release-check: plugin.yaml has no top-level version: line" >&2
  exit 1
fi
if [[ "$manifest_version" != "$version" ]]; then
  echo "release-check: tag $tag names version $version, but plugin.yaml says $manifest_version" >&2
  exit 1
fi

symbol="${FAKE_VERSION_SYMBOL:-github.com/infrata/infrata-provider-fake/internal/fake.Version}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 go build -trimpath -ldflags "-X ${symbol}=${version}" -o "$work/infrata-plugin-fake" ./cmd/infrata-plugin-fake

# The binary refuses to start without the host's cookie. With it and an empty stdin, it writes
# its handshake {"protocol","name","version"} and exits. Captured whole, not piped to head,
# so pipefail cannot turn an early-closed pipe into a false failure.
out="$(INFRATA_PLUGIN_COOKIE=release-check "$work/infrata-plugin-fake" </dev/null 2>/dev/null)"
handshake="${out%%$'\n'*}"
reported="$(printf '%s' "$handshake" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
if [[ "$reported" != "$version" ]]; then
  echo "release-check: the binary reports version '${reported}', not $version (handshake: $handshake)" >&2
  echo "The -ldflags symbol ${symbol} does not set internal/fake.Version; every archive would report the wrong version." >&2
  exit 1
fi
echo "release-check: tag, plugin.yaml and binary all say $version"
```

- [ ] **Step 4: Write `scripts/build-release`** (mode 0755)

```bash
#!/usr/bin/env bash
# build-release VERSION OUTDIR: cross-compile infrata-plugin-fake for every platform plugin.yaml
# lists, archived under the name `infrata plugins install` constructs (infrata PLAN.md §31.2):
# infrata-plugin-fake_<version>_<goos>_<goarch>.tar.gz, and .zip for windows.
#
# PLATFORMS (space-separated GOOS/GOARCH) overrides the manifest's list, so a test can build two
# platforms instead of eight.
set -euo pipefail

version="${1:?usage: scripts/build-release VERSION OUTDIR}"
out="${2:?usage: scripts/build-release VERSION OUTDIR}"
cd "$(dirname "$0")/.."
mkdir -p "$out"
out="$(cd "$out" && pwd)"

platforms="${PLATFORMS:-$(sed -n 's/^platforms:[[:space:]]*\[\(.*\)\][[:space:]]*$/\1/p' plugin.yaml | tr ',' ' ')}"
if [[ -z "${platforms// /}" ]]; then
  echo "build-release: plugin.yaml has no single-line platforms: [...] list" >&2
  exit 1
fi

for platform in $platforms; do
  goos="${platform%/*}"
  goarch="${platform#*/}"
  name="infrata-plugin-fake"
  if [[ "$goos" == windows ]]; then name="$name.exe"; fi
  goarm=""
  if [[ "$goarch" == arm ]]; then goarm=7; fi   # R10: archived as _linux_arm, per §31.2's convention
  stem="infrata-plugin-fake_${version}_${goos}_${goarch}"

  work="$(mktemp -d)"
  mkdir "$work/$stem"
  echo "==> $platform"
  # CGO_ENABLED=0: a static binary that runs without a matching libc. -trimpath: no build-machine
  # paths. Not -s -w: a stack trace is the whole diagnostic when a plugin panics.
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
    go build -trimpath \
      -ldflags "-X github.com/infrata/infrata-provider-fake/internal/fake.Version=${version}" \
      -o "$work/$stem/$name" ./cmd/infrata-plugin-fake
  cp README.md plugin.yaml "$work/$stem/"
  if [[ "$goos" == windows ]]; then
    (cd "$work" && zip -qr "$out/$stem.zip" "$stem")
  else
    tar -czf "$out/$stem.tar.gz" -C "$work" "$stem"
  fi
  rm -rf "$work"
done
```

- [ ] **Step 5: Change the `Version` default (R9)**

In `internal/fake/plugin.go` replace the `Version` declaration and its comment with:

```go
// Version is reported in the handshake and checked against a project's `plugins:`
// constraint. It is "0.0.0-dev" in every build a release did not stamp: a checkout
// build claiming to be a release is how a bug report turns into an afternoon, and a
// default equal to plugin.yaml's version would let a broken -ldflags path pass the
// release check. scripts/build-release stamps it:
// -ldflags "-X github.com/infrata/infrata-provider-fake/internal/fake.Version=1.2.3".
var Version = "0.0.0-dev"
```

- [ ] **Step 6: Run to verify pass**

Run: `chmod +x scripts/release-check scripts/build-release && gofmt -l . && go vet ./... && go test -count=1 ./...`
Expected: `ok` for `internal/fake` and `scripts`.

- [ ] **Step 7: Write `.github/workflows/release.yml`**

```yaml
name: Release

# Triggered by a version tag. Mirrors infrata's own release workflow: one runner cross-compiles
# every platform, the version is stamped by -ldflags and nowhere else, and nothing is published
# unless the tag, plugin.yaml's version and the binary's reported version agree (PLAN.md §31.2).

on:
  push:
    tags: ["v*"]

permissions:
  contents: read

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write # to create the release
    env:
      GOTOOLCHAIN: local
    steps:
      # go.mod replaces github.com/infrata/infrata with ../ilan, so both repositories are checked
      # out side by side and every step runs from this one's directory.
      - uses: actions/checkout@v4
        with:
          path: infrata-provider-fake
      - uses: actions/checkout@v4
        with:
          repository: infrata/infrata
          path: ilan
          # infrata is not public yet; a token with read access to it. Remove once it is.
          token: ${{ secrets.INFRATA_CHECKOUT_TOKEN }}

      - uses: actions/setup-go@v5
        with:
          go-version: "1.27"

      - name: Verify the tag, the manifest and the binary agree
        working-directory: infrata-provider-fake
        run: scripts/release-check "$GITHUB_REF_NAME"

      - name: Test, including the compliance suite against infrata
        working-directory: infrata-provider-fake
        run: |
          set -euo pipefail
          test -z "$(gofmt -l .)"
          go vet ./...
          go test -count=1 ./...
          go test -tags e2e -count=1 ./e2e/

      - name: Build every platform in plugin.yaml
        working-directory: infrata-provider-fake
        run: scripts/build-release "${GITHUB_REF_NAME#v}" dist

      - name: Checksums
        working-directory: infrata-provider-fake/dist
        run: sha256sum ./*.tar.gz ./*.zip > SHA256SUMS && cat SHA256SUMS

      - name: Publish the release
        working-directory: infrata-provider-fake
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh release create "$GITHUB_REF_NAME" --title "$GITHUB_REF_NAME" --generate-notes --verify-tag dist/*
```

The workflow cannot run here: there is no git remote. Check what can be checked locally, and record
each result in the report:
- The file parses as YAML: `python3 -c 'import sys,yaml; yaml.safe_load(open(sys.argv[1]))' .github/workflows/release.yml`.
  If PyYAML is absent, say so. Do not add a dependency to get it.
- Run the release steps by hand, in order:
  `scripts/release-check v0.1.0 && scripts/build-release 0.1.0 /tmp/fake-dist && (cd /tmp/fake-dist && sha256sum ./*.tar.gz ./*.zip)`.
  Expected: 8 archives, including `infrata-plugin-fake_0.1.0_linux_arm.tar.gz` and two `.zip`s.
- Unpack the `linux_amd64` archive, run its binary with the cookie and empty stdin, and confirm the
  handshake reports `0.1.0`.

- [ ] **Step 8: Sabotage**

One at a time; confirm the named test fails; revert by editing back:
- `release-check`: `if [[ "$manifest_version" != "$version" ]]` → `if false` → `TestReleaseCheckRefusesATagTheManifestDoesNotName` (the check then builds 99.0.0, which agrees with itself).
- `release-check`: `if [[ "$reported" != "$version" ]]` → `if false` → `TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion`.
- `plugin.go`: `var Version = "0.0.0-dev"` → `var Version = "0.1.0"` → `TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion` (the unstamped binary now matches the manifest by accident). This is R9's evidence.
- `build-release`: `if [[ "$goos" == windows ]]; then` (the archive branch) → `if false; then` → `TestBuildReleaseNamesArchivesByTheInstallConvention` (no `.zip`).

- [ ] **Step 9: Commit**

```bash
git add plugin.yaml scripts/release-check scripts/build-release scripts/scripts_test.go .github/workflows/release.yml internal/fake/plugin.go
git commit -m "release: plugin.yaml, and a release that refuses a version disagreement

infrata's §31.2 makes the manifest authoritative and requires a release to fail unless the
tag, the manifest's version and the binary's reported version agree; a drift test is
something a person can delete, a release gate is not. The unstamped default becomes
0.0.0-dev, because a default equal to the manifest would let a broken -ldflags path pass.
Archives follow the name install will construct.
Sabotage-verified: manifest comparison, binary comparison, dev default, windows archive." -- plugin.yaml scripts/release-check scripts/build-release scripts/scripts_test.go .github/workflows/release.yml internal/fake/plugin.go
```

---

### Task 12: Validate `plugin.yaml` with infrata's parser (GATED)

Waits on infrata publishing a manifest parser (requested 2026-09-13, user decision). When it exists,
this task is written against its real API: a normal-suite test that parses `plugin.yaml` with it,
validates it, and asserts `name == PluginName` and `protocol` contains `pluginproto.Version`; plus an
e2e subtest that `infrata version --output`'s `plugin protocol` set intersects `protocol`. It is not
specified further here, because an API that does not exist yet cannot be written against honestly.

---

### Task 7b: README — discovery, the compliance suite, and releasing

Added 2026-09-13. Task 7 ran under scope rulings while infrata's discovery was broken (fixed in infrata
`de33b4d`) and before the e2e suite (Task 6) and the release gate (Task 11) were committed. This task
adds what those rulings held back, and fixes one wording finding from Task 7's review. Runs after
Tasks 6 and 11 are complete.

**Files:**
- Modify: `README.md`, `internal/fake/readme_test.go`

**Interfaces:**
- Consumes: `e2e/testdata/basic/infra.yml` and `e2e/e2e_test.go` (Task 6); `plugin.yaml`,
  `scripts/release-check`, `scripts/build-release`, `.github/workflows/release.yml`, `Version` default
  `0.0.0-dev` (Task 11).
- Produces: nothing code depends on.

- [ ] **Step 1: Write the failing test**

In `internal/fake/readme_test.go`, extend the `want` list in `TestReadmeQuotesTheTestedExample` with:
`"infrata discover"`, `"import dev fake.network."`, `"-tags e2e"`, `"INFRATA_SRC"`, `"plugin.yaml"`,
`"scripts/release-check"`, `"0.0.0-dev"`.

- [ ] **Step 2: Run to verify failure**

Run: `go test -count=1 -run TestReadmeQuotesTheTestedExample ./internal/fake/`
Expected: FAIL — `README.md never mentions` each of the new strings.

- [ ] **Step 3: Capture real output**

Build both binaries into `bin/` from infrata HEAD (must include `de33b4d`; record `git -C ../ilan log --oneline -1`).
In a fresh temp project copied from `e2e/testdata/basic/infra.yml`: `apply dev --auto-approve`; add by
hand to `.infra/fake-cloud.json` a resource `"net-77": {"type": "fake.network", "attributes": {"cidr": "172.16.0.0/12", "id": "net-77"}}`
(no `address`); run `infrata discover`, then `infrata import dev fake.network.net-77 --generate`, then
`cat discovered/*.yml`, then `infrata plan dev`. Keep all four outputs and exit codes.

- [ ] **Step 4: Edit README.md**

1. **The cloud file section.** Keep README.md's existing sentence about hand-added resources, and follow it
   with the Step 3 `discover` and `import … --generate` commands and output, the generated file, and
   the clean `plan` that follows.
2. **Injecting failures.** Correct the `seen`/`fired` sentence: `seen` counts matching calls and is written
   back on every one, whether or not the rule fires; `fired` is set when it fires. Delete both to re-arm a rule.
3. **Development.** Add the compliance suite: `go test -tags e2e -count=1 ./e2e/`, which builds infrata
   from `$INFRATA_SRC` (default `../ilan`) and this plugin, and skips with an `E2E SKIPPED:` line when
   the source is absent. Run it before a release.
4. **New section "Releasing", after Development.** `plugin.yaml` at the repo root (infrata `PLAN.md`
   §31.2): quote the file as committed, and say it is read at a release tag, never the default branch.
   `Version()` reports `0.0.0-dev` in any build a release did not stamp. A `v*` tag runs
   `.github/workflows/release.yml`: `scripts/release-check` refuses a tag, manifest and binary that
   disagree; `scripts/build-release` builds every platform in `plugin.yaml` as
   `infrata-plugin-fake_<version>_<goos>_<goarch>.tar.gz` (`.zip` for windows); then `SHA256SUMS`.
   Show `scripts/release-check v0.1.0` run locally, with its real output.
5. **Try it / Upgrading.** No change, unless a real command in them no longer matches its output (re-run
   them; fix any that drifted and say so in the report).

- [ ] **Step 5: Run to verify pass, and try the new parts cold**

Run: `go test -count=1 ./...`
Expected: `ok`. Then follow the new cloud-file and Releasing commands literally from the README in a fresh
temp directory; each must print what the README shows.

- [ ] **Step 6: Sabotage**

- Delete the Releasing section → `TestReadmeQuotesTheTestedExample` names `plugin.yaml`, `scripts/release-check`, `0.0.0-dev`.
- Delete the discover/import example → the same test names `infrata discover` and `import dev fake.network.`.

- [ ] **Step 7: Commit**

```bash
git add README.md internal/fake/readme_test.go
git commit -m "docs: README covers discovery, the compliance suite and releasing

Task 7 held these back while discovery was broken in infrata and before the suite and
the release gate existed; all three are real now, so the README shows them with real
output, and a test fails if any of them disappears. Also corrects when seen and fired
are written back.
Sabotage-verified: releasing section, discover/import example." -- README.md internal/fake/readme_test.go
```
