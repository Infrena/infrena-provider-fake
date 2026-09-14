# AGENT.md — writing an infrata provider plugin

A guide for building an infrata provider plugin, written to be used by a coding agent. It is
portable: nothing below is specific to the fake provider, so copy this file into your own plugin
repository and work from it.

**The one-line version.** A plugin is an ordinary Go program whose `main()` is a single call, and
whose job is to implement two interfaces. Infrata launches it as a child process and talks to it
over stdin/stdout in newline-delimited JSON. You never write any of that transport.

```go
package main

import (
	"github.com/infrata/infrata/pkg/pluginsdk"
	"github.com/example/infrata-plugin-hetzner/internal/hetzner"
)

func main() { pluginsdk.Main(hetzner.New()) }
```

Contract, in the infrata repository: `PLAN.md` §31.1 (the design and its reasoning) and §31.2 (the
plugin manifest), `pkg/pluginproto` (the wire), `pkg/pluginsdk` (`Main`), `pkg/provider` (the
interfaces), `pkg/schema` (describing types), `pkg/value` (the value model), `pkg/plugintest` (the
test harness in §8), `pkg/semver` (the constraint syntax in §9).

---

## 1. The shape of a plugin

### Naming, which is not cosmetic

| Thing | Rule |
| --- | --- |
| Binary name | `infrata-plugin-<name>` (`.exe` on Windows). This is how it is found. |
| `Plugin.Name()` | must equal `<name>`. The host refuses a mismatch. |
| Resource types | must be prefixed `<name>.` — plugin `hetzner` serves `hetzner.server`. |

The prefix rule is enforced on load, and it is load-bearing rather than tidy: without it two plugins
could both claim one type name, and which one won would depend on load order. A type name also tells
a reader where a resource came from.

`Plugin.Name()` returning something other than `<name>` is refused with a message naming both,
because a renamed or mis-copied binary otherwise serves the wrong schemas — and the first sign of
that is a plan proposing something nobody asked for.

### Where the binary goes

Infrata looks for it in this order, first match wins:

1. `--plugin-dir`, then `INFRATA_PLUGIN_PATH`
2. `<project>/.infra/plugins/`
3. `~/.local/share/infrata/plugins/`
4. `$PATH`

`--verbose` prints which path each plugin was loaded from. For development, `--plugin-dir` pointing
at your build output is the shortest loop.

### Running it by hand

Don't — and it will tell you so. The host sets `INFRATA_PLUGIN_COOKIE`, and without it the SDK
prints a line saying what the binary is and exits non-zero. That check exists because a protocol
program started on a terminal otherwise sits silently waiting for input on stdin, which is
indistinguishable from a hang.

### Depending on infrata

`github.com/infrata/infrata` is not published as a fetchable module (it stays private until it is
feature complete), so a plugin needs a `replace` directive in its `go.mod` pointing at a checkout.
Clone infrata next to your plugin and point at the directory `git clone` creates, so a fresh clone of
both repositories builds with no extra setup:

```
replace github.com/infrata/infrata => ../infrata
```

Nothing else is needed: the SDK and everything it depends on is the standard library only, so
there is no other third-party dependency to pull in.

**A `replace` builds against whatever is on disk in `../infrata`, committed or not.** A green suite
proves nothing about committed infrata. Build releases in CI from fresh checkouts of both
repositories. Because infrata is private, CI needs a token to check it out beside your plugin:
infrata-provider-fake's `.github/workflows/release.yml` uses an `INFRATA_CHECKOUT_TOKEN` secret, which
should be a fine-grained token scoped to Contents: read-only on `infrata/infrata`.

### Keep your module path outside infrata's

**Your module path must not be under `github.com/infrata/infrata/`.** Go's `internal/` rule is checked
by import path, not by module. A nested module named `github.com/infrata/infrata/providers/x` compiles
while importing `github.com/infrata/infrata/internal/pluginhost`, while the same import from
`example.com/x` fails with `use of internal package … not allowed` (infrata `PLAN.md` §31.1, "Where
the code lives"). A plugin under infrata's path can quietly depend on engine internals no other plugin
has. `github.com/infrata/infrata-provider-aws` is outside that path, and correct.

Your module's own `go` directive must be at least infrata's own — 1.27 as of 2026-09-13. With
`GOTOOLCHAIN=auto` (the default), Go just fetches a new enough toolchain; with a local toolchain
too old to build it, the build fails with `go: <module>@<version> requires go >= X (running go Y;
GOTOOLCHAIN=local)`, which does name the module whose requirement it is.

---

## 2. The two interfaces

### `provider.Plugin` — the plugin itself

```go
type Plugin interface {
	Name() string
	Definitions() []*schema.ResourceDefinition
	New(cfg provider.Config) (Provider, error)
}
```

`Config` is a struct, not a bag of parameters: `Instance` (the name the user gave this instance),
`Values` (the instance's own resolved configuration — nothing reserved lives in here; every key came
from the user), and `ProjectDir` (the project directory, for resolving a relative path from
configuration). `cfg.Value(key)` looks one up. It is a struct on purpose: a plugin built against an
older SDK must keep compiling as this grows, and a new field on a struct is additive while a new
parameter on a function is not — plugins are compiled by other people, on their own schedule.

- **`Definitions` must answer with no configuration at all.** Schemas are static: `hetzner.server`
  is described the same way whichever account it would be created in. This is what lets infrata
  compile a project before it knows any credentials, and it is the whole reason the interface is
  split in two.
- **`New` is called once per configured instance**, with that instance's own resolved configuration.
  One process serves many instances: two accounts of one cloud means one plugin process holding two
  configured clients. `cfg.Instance` is the name the user gave it, and a plugin whose configuration
  is entirely optional still needs it to keep two instances apart.
- **An error from `New` is a user's problem to fix** — a missing credential, an unreadable path, a
  key you do not accept — so say what is wrong and what to do. It is rendered as a configuration
  error against the `providers:` entry that caused it.
- Optionally implement `Version() string`. It is reported in the handshake, shown by `--verbose`,
  and checked against a project's `plugins:` constraint. A plugin that does not track versions
  reports `0.0.0` and still works.

**Fail closed on configuration you do not understand.** A misspelled key that is silently ignored
means an instance quietly sharing another's account, and the first sign of that is a plan proposing
to destroy resources somebody else owns. Reject unknown keys, and name both what was written and
what you accept.

### `provider.Provider` — one configured instance

```go
type Provider interface {
	Name() string
	Definitions() []*schema.ResourceDefinition

	Read(ctx context.Context, current *resource.ResourceState) (*resource.ResourceState, error)
	Create(ctx context.Context, desired *resource.DesiredResource) (*resource.ResourceState, error)
	Update(ctx context.Context, current *resource.ResourceState, desired *resource.DesiredResource) (*resource.ResourceState, error)
	Delete(ctx context.Context, current *resource.ResourceState) error

	Discover(ctx context.Context, req DiscoverRequest) ([]DiscoveredResource, error)
	Import(ctx context.Context, resourceType, id string) (*resource.ResourceState, error)

	ClassifyError(err error) Retryability
}
```

| Method | Returns | Notes |
| --- | --- | --- |
| `Read` | current state, or `(nil, nil)` if it no longer exists | `(nil, nil)` is how a deleted resource is reported; it is not an error |
| `Create` | the created resource, **never `(nil, nil)`** | see below |
| `Update` | the updated resource, **never `(nil, nil)`** | make the resource match `desired` — including removing what `desired` no longer has; `desired` never contains computed attributes, so keep those |
| `Delete` | error only | deleting something already gone should succeed |
| `Discover` | everything that exists of the requested types | including resources infrata does not manage. `DiscoverRequest.Region` is never set by the host: it is always `""`, so don't implement against it |
| `Import` | one resource by the cloud's own ID | `(nil, nil)` becomes "no such resource" |

**Never return `(nil, nil)` from `Create` or `Update`.** The host turns it into an error saying the
resource may exist untracked, because that is the only honest reading: a nil result with no error is
indistinguishable from "nothing happened", and if the call did take effect then a real resource now
exists that nothing points at — unfindable by a later plan or destroy. If you cannot determine the
created state, return an error saying that.

Return `ErrNotImplemented` for a capability you do not offer, and say so in `Capabilities`.

---

## 3. Describing resource types

```go
&schema.ResourceDefinition{
	Type:        "hetzner.server",
	Description: "A cloud server.",
	Attributes: map[string]schema.Attribute{
		"name":        {Kind: value.KindString, Required: true},
		"server_type": {Kind: value.KindString, Required: true, ForceNew: true},
		"image":       {Kind: value.KindString, Required: true, ForceNew: true},
		"backups":     {Kind: value.KindBool, Default: false},
		"labels":      {Kind: value.KindMap, Description: "Free-form labels"},
		"root_password": {Kind: value.KindString, Sensitive: true, Computed: true},
		"ipv4":        {Kind: value.KindString, Computed: true},
	},
	Requirements: []schema.Requirement{{
		Name:        "network",
		Types:       []string{"hetzner.network"},
		Description: "A server must sit inside a network",
	}},
	Capabilities: schema.Capabilities{Create: true, Read: true, Update: true, Delete: true, Import: true},
	ImportID:     schema.ImportSpec{Description: "the numeric server ID, e.g. 42"},
}
```

### Each flag, and what it causes

| Flag | Meaning | What it causes downstream |
| --- | --- | --- |
| `Required` | configuration must supply it | a clear error before anything is planned, naming the resource |
| `Computed` | the cloud assigns it; configuration may not set it | shows as `(known after apply)` in a plan; a user who sets it gets "is computed and cannot be set" |
| `Sensitive` | it is a secret | redacted everywhere — plans, reports, generated configuration |
| `ForceNew` | changing it replaces rather than updates | the plan says *replace*, loudly, instead of *update* |
| `Default` | a **datum** of the declared `Kind` | filled in when absent, marked `[default]`, and omitted from generated configuration |

**`Default` is a value, not a function.** A schema has to survive a pipe. If a default seems to need
the environment or the region, it is really either a user's variable (they decide, and can see it) or
a computed attribute (you report it).

Get `ForceNew` right. It is the difference between a plan that says *update* and one that says
*destroy and recreate*, and a user approves on the strength of that word.

Get `Sensitive` right too, but know that the host does not depend on you for it: sensitivity is
forced from the schema onto every value you return, so a forgotten flag on a value is caught. A
missing flag on the **schema** is not caught, because nothing else knows.

`Requirements` is what gives a user missing-dependency detection: infrata reports what is missing,
with a suggested fix, instead of letting your API call fail.

---

## 4. What the host does to your results, so you don't have to

Infrata does not take a plugin's word for these. Each was once a rule in a doc comment that every
provider had to remember; a binary somebody else built cannot be held to a comment, so the host
enforces them for every plugin.

**Knowing this list saves you work.** Do not reimplement any of it: a plugin that also does these
things has tests that pass when the host is broken.

1. **Bookkeeping is never sent to you, so you cannot drop it.** You receive the type, the provider
   ID, the attributes, and the resource's address — the address as an *input*, for tagging, naming
   or an error message. Dependencies, lifecycle flags and timestamps are never sent. On anything you
   return, everything but the provider ID and attributes — the address included — is ignored and
   re-attached by the host from what it already holds. You do not need to carry anything forward
   from `current` — and `current` is given to `Read` and `Update` so you can *use* it, not so you
   can copy it back.
2. **Sensitivity is forced from the schema.** A value you return for a `Sensitive` attribute is
   marked whether or not you marked it.
3. **Provenance is the host's.** Every value you return is recorded as coming from the provider. You
   cannot claim a user wrote something.
4. **`(nil, nil)` from `Create`/`Update` becomes an error**, as above.
5. **An attribute your own schema does not declare is refused**, not persisted. So a typo in a
   returned key is an error naming it, rather than a mystery line in a plan.
6. **Your schemas are validated on load**, including the type prefix rule and the reserved
   attribute names `prevent_destroy` and `retain` (which belong to infrata's lifecycle handling, so
   no attribute may be called either).
7. **Error classification travels with your error.** See below.

---

## 5. Errors and retries

```go
func (p *provider) ClassifyError(err error) Retryability
```

Return one of `provider.NotSafeToRetry`, `provider.ConditionallyRetryable`, `provider.SafeToRetry`.
Infrata owns the backoff; you only classify.

- **`SafeToRetry`** — the operation provably did not take effect: a throttle, or a 5xx your API
  guarantees was rejected before doing anything. Infrata retries a create, update or delete that
  fails this way.
- **`ConditionallyRetryable`** — it may have taken effect: a timeout, or a connection dropped after
  the request was sent. Infrata retries only an update that fails this way; a create or a delete is
  **not** retried, because a second attempt could make a duplicate or act on something else.
- **`NotSafeToRetry`** — a validation failure, a permissions problem, anything that will fail the
  same way again, or anything you are unsure about. Never retried. **This is the right default when
  you are unsure.**

Reads, discovery and import are not retried by infrata under any classification; a plugin may retry
a transient read failure itself. `docs/writing-a-provider.md` §5 has the per-operation table.

`ClassifyError` takes an `error`, and an `error` cannot cross a pipe — so the SDK calls your
`ClassifyError` on your side and sends the answer along with the message. You do not need to do
anything for this to work; it is worth knowing because it means your classification is consulted
exactly once, at the moment the error is produced. An error that did not come from you at all — a
broken pipe, a crashed plugin — is classified `NotSafeToRetry` by the host, because that is exactly
the case where retrying a create could make a second resource.

**Error messages are a product feature.** Say what is wrong, what was expected, and what to do.
`"401"` is not an error message; `"the API rejected the token (401): check HCLOUD_TOKEN is set and
has write scope"` is.

---

## 6. Logging, credentials, and the two streams

- **stdout is the protocol.** Never write to it. A single stray `fmt.Println` corrupts the stream
  and every message after it, and the symptom is a confusing parse error much later, in an unrelated
  operation. The SDK points `os.Stdout` at stderr to catch the common case, but a direct write to
  fd 1 still escapes.
- **stderr is your log.** Infrata prefixes each line with your plugin's name and shows it under
  `--verbose`. It also keeps the last lines, so when a plugin exits unexpectedly the error quotes
  what it said on the way out. That tail is often the only thing that explains a crash — a plugin
  that dies of a missing credential otherwise shows up only as "the plugin stopped responding" (the
  host replaces the bare `EOF` a closed pipe leaves behind with that sentence).
- **Never log a credential, a token, or the value of a `Sensitive` attribute.** stderr is shown to
  users and captured in CI logs. The redaction machinery protects values that travel through
  infrata; it cannot protect what you print yourself.
- Take credentials the way your cloud's own tooling does — the standard environment variables and
  config files — so that a user who can already use their cloud's CLI does not have to configure
  anything twice. Accept explicit configuration in `providers:` as an override, and remember it may
  come from a variable, so it can differ per environment. (This is current infrata design — see
  §10: `plan`, `apply`, `refresh`, `destroy` and `import <env>` all resolve those variables against
  the environment named on the command line. `discover` alone takes no environment, and resolves
  only what doesn't need one, refusing anything else by name.)

---

## 7. The project directory

`cfg.ProjectDir` is there when you need to resolve a relative path from configuration — a file the
user named relative to their project rather than to whatever working directory the plugin inherited.
Most real plugins do not need it. If yours does, resolve against it rather than against the
process's `cwd`, which is not the project.

**Use an absolute path from configuration as written.** Joining it onto the project directory
anyway silently rebases it — `cloud: /tmp/x.json` becomes `<project>/tmp/x.json`, a bug the fake
provider had. Check `filepath.IsAbs` first and only join when it's false.

---

## 8. Testing a plugin

You do not need a cloud account, and you do not need a subprocess.

- **Test your `Provider` directly.** It is an ordinary Go interface; call its methods in a table
  test. This is where most of your coverage belongs.
- **Test the protocol path with `pkg/plugintest`.** It runs the SDK on one end of an in-memory pipe
  and infrata's own host on the other, so every call is encoded, decoded and passed through the
  host's real trust rules with no subprocess involved:

  ```go
  host, err := plugintest.Open(ctx, myplugin.New(), t.TempDir())
  if err != nil { t.Fatal(err) }            // schemas refused on load
  defer host.Close()
  prov, err := host.Configure(provider.Config{Instance: "main"})
  ```

  `Open` fails if your schemas do not pass the checks infrata applies on load — the type prefix
  rule, a reserved attribute name, a default of the wrong kind — which makes it worth a test of its
  own. `Configure` returns the host's own adapter, so schema validation, forced sensitivity,
  provenance, undeclared-attribute refusal and error classification all apply to what it returns,
  exactly as they would through a subprocess.

  **Do not assert in a unit test that your `Provider` marks sensitivity or carries bookkeeping
  forward — it should not; assert that the host does it, through `plugintest`.** See
  infrata-provider-fake's `internal/fake/protocol_test.go` for a worked example.
- **Test the binary once.** One end-to-end test that builds the binary and runs a real `infrata`
  against it is enough to prove the packaging; everything else is faster and clearer at the two
  levels above. Keep it behind a build tag if it builds infrata itself, so the rest of the suite
  does not pay the cost of building the infrata CLI from source. See infrata-provider-fake's `e2e/`
  for a worked example.
- **Fake the cloud, not your own code.** Point your plugin at a test double of your cloud's API — a
  `httptest.Server`, or an interface you implement twice — rather than mocking your own methods. A
  test that mocks the thing under test asserts nothing.

**Sabotage every test you write.** Break the code it covers and confirm the test fails. A sabotage
must leave the code *compiling* and change *behaviour* — one that breaks the build discriminates
nothing, and a test that passes against broken code is worse than no test because it is evidence
that does not exist.

---

## 9. Releasing

- Build for every platform your users have. A plugin is a plain Go binary, so this is
  `GOOS`/`GOARCH` and nothing more.
- Name the artefact `infrata-plugin-<name>` and ship it as-is; users put it on the search path.
- Report a real version from `Version()`, so a project can pin it (see "Constraints" below).
- **The protocol version is the compatibility contract, not the Go types you compiled against.** A
  plugin built against an older SDK keeps working for as long as its protocol version is supported.
  You do not have to rebuild for every infrata release.

### The manifest

Every plugin repository ships a `plugin.yaml` at its root (infrata `PLAN.md` §31.2):

```yaml
manifest: 1
name: hetzner
version: 1.2.0
protocol: [1]
platforms: [linux/amd64, linux/arm64, darwin/arm64, windows/amd64]
description: A provider for Hetzner Cloud.
infrata: ">= 0.2.0"
source: https://github.com/example/infrata-plugin-hetzner
```

`manifest` (checked first, before any other key), `name`, `version`, `protocol` (a list — the host
accepts a set of supported versions), `platforms` (one `GOOS/GOARCH` per build you publish) and
`description` are required. `infrata` is optional — a `pkg/semver` constraint on the infrata
releases this plugin is known to work with; absent means unconstrained, never write `">= 0.0.0"` to
say that. `source` is optional, for a search result to link.

It is read at the release **tag**, never the default branch: the file on `main` describes unreleased
code, and reading it to judge `v1.2.0` would answer the wrong question once `main` has moved on to
describing `v1.3.0`.

Deliberately absent, and don't add them back: checksums (`SHA256SUMS` is a release asset, built
after the binaries exist, so a checked-in manifest cannot carry one honestly), asset names or
download URLs (a convention instead — `infrata-plugin-<name>_<version>_<goos>_<goarch>.tar.gz`,
`.zip` on Windows — one convention beats a field every author can get wrong), and resource types
(`name` already implies them: a plugin serves `<name>.*` and the host refuses anything else).

See infrata-provider-fake's `plugin.yaml` for a worked example. Validate your own manifest with
`pkg/pluginmanifest.Parse` — the same parser `infrata plugins install` will use — rather than
trusting a hand check that a typo could pass.

### The release gate

A release must **fail** unless three things agree: the git tag, `plugin.yaml`'s `version`, and the
version the built binary actually reports in its handshake. A drift test that merely compares the
manifest to the code is a weaker substitute — it is a test someone can delete, where the release
assertion blocks the release outright.

Make `Version()` default to something like `0.0.0-dev`, never a value equal to the manifest's
version, and stamp the real version only at release time with `-ldflags -X`. If the default already
matched the manifest, a broken `-ldflags` path would pass the gate silently — the binary would
report the right version by coincidence, not because the release stamped it.

See infrata-provider-fake's `scripts/release-check`, `scripts/build-release` and
`.github/workflows/release.yml` for a worked example of a gate built this way.

### Constraints

A project pins your plugin with:

```yaml
plugins:
  hetzner: ">= 1.2.0, < 2.0.0"
```

The constraint is checked against the version your **handshake** reports, not anything else. A
plugin that does not implement `Version()` reports `0.0.0`; an unstamped build reports its default,
such as `0.0.0-dev`, which compares as `0.0.0` because pre-release suffixes are ignored. Either
cannot satisfy any constraint above `0.0.0`.

The syntax is `pkg/semver`'s: comparison operators `>= <= != == > < =` on `MAJOR.MINOR.PATCH`, comma
meaning AND, a bare version pinning exactly, `0.4` meaning `0.4.0`, and any pre-release suffix
ignored for comparison purposes.

---

## 10. A real cloud

The rules a real cloud adds, condensed. `docs/writing-a-provider.md` §14 has the reasoning, the
evidence, and AWS worked through as an example.

- **Your cloud's SDK is a fine dependency** in your own module. Keeping such SDKs out of infrata's
  core is one reason plugins are separate processes.
- **Credentials:** load them the way your cloud's own tooling does (for AWS, the SDK's
  `config.LoadDefaultConfig`), take overrides such as a profile or a role as named keys in the
  instance's configuration, refuse unknown keys, and make `New`'s error say what was tried and what to
  set. One instance per account.
- **`config` and `defaults:` are different maps.** Every key other than `plugin`, `name`, `default`
  and `defaults` in a `providers:` entry reaches `New` as `cfg.Values`. `defaults:` holds attribute
  defaults for resources, is applied by infrata at compile time, and is **never sent to the plugin**.
- **Regions: a default on the instance, overridden per resource.** Declare `region` on every regional
  type as `Required` + `ForceNew`. Users set `defaults: {region: …}` once and override it with a
  resource's own `region:`. Keep one SDK configuration and a client per region.
- **Changing an instance's default region replaces every resource that relies on it.** `region` is
  `ForceNew`, and a resource that omits `region:` gets it from `defaults:`. Editing
  `defaults: {region: …}` therefore plans a destroy-and-create of every resource that inherited the
  old default. Set `region:` explicitly on any resource that must not move before changing the
  default. For a stateful resource that must never be replaced this way, infrata's
  `lifecycle: prevent_destroy: true` turns that plan into a refusal instead.
- **Don't rely on a region from infrata.** `DiscoverRequest.Region` is always `""`, and `${region}`
  is not a variable: it fails with `undefined variable "region"`. The regions `Discover` scans come
  from a configuration key, such as `discover_regions` — keep it resolvable without an environment
  (a literal, a variable with a `default:`, or one set in `vars/default.yml`), because `discover`
  takes no environment; a value only an environment sets needs `--var` on `discover` itself.
- **`providers:` variables resolve on `plan`, `apply`, `refresh`, `destroy` and `import <env>`,
  against the environment named on the command line** (infrata `PLAN.md` §12.1, amended
  2026-09-13; `import`'s own environment fixed in the same amendment, `internal/cli/context.go`'s
  `discoveryRegistry`). `refresh` and `destroy` accept `--var`/`--var-file`, having refused them
  outright before. `discover` alone builds its instance with NO environment at all — it is the one
  command that has none to give — so a `providers:` key that only an environment sets is refused BY
  NAME on `discover`, naming the key and suggesting `--var`. `plan`/`apply` against an orphaned
  environment (removed from `environments:` but still holding state) take that same no-environment
  path. Give a per-environment `providers:` value a `default:` or a `vars/default.yml` entry if
  bare `discover` must resolve it too, or pass `--var` to `discover` itself.
- **Discover:** only the requested types, one API family per type; paginate; check `ctx` between
  pages; include resources infrata did not create.
- **Import:** `infrata import` picks from what `Discover` returned, matched as
  `<type>.<provider id>`. So use one ID form everywhere, carrying anything `Import` needs that isn't in
  the ID alone (for AWS, `<region>/<id>`). Refuse an ID of the wrong type. A selector names no
  provider instance, so a provider ID your plugin issues twice across two instances (two accounts) is
  refused by `import` as ambiguous rather than silently resolved — `--provider <instance>` is the
  user's way to narrow it. Keep IDs unique within an account; a cross-account collision is a real risk
  to design around, since the selector still can't tell instances apart.
- **Classify SDK errors against §5:** a throttle AWS refused before acting → `SafeToRetry`; a server
  fault, timeout or connection lost after sending → `ConditionallyRetryable`; validation, access
  denied, not-found on a mutation, anything unrecognised → `NotSafeToRetry`.
- **Don't stack retry loops blindly.** Cloud SDKs retry by default, and infrata retries too. Let the
  SDK retry reads, which infrata never does. Don't let it silently resend a create that has no
  idempotency token.
- **Eventual consistency:** build `Create`'s result from the create response. A `Read` that returns
  `(nil, nil)` makes the next plan propose creating the resource again, so don't report a
  just-created resource as gone on a single `NotFound`: retry briefly, or return an error.
- **Write-only attributes** the API never returns, such as a database master password: carry them
  forward from `current` in `Read`. Otherwise every plan proposes an update.
- **Requirements** are satisfied by any resource of a listed type in the SAME provider instance, not
  by state, and not region-aware (a subnet's `vpc` requirement is satisfied by a VPC in any region of
  that instance) — corrected 2026-09-13, `internal/compiler/validate.go`'s `checkRequirements`.
  Declare what the cloud would reject a create without.
- **Recommendation: `Requirements` is a pre-flight hint, not the correctness mechanism.** It cannot
  trace a specific edge — a `Requirement` names no attribute — so still model the dependency as a
  `Required` reference attribute too (`vpc_id: ${vpc.id}`), which is what infrata actually validates
  and enforces. Treating the hint as the guarantee leaves references under-specified: a project can
  pass `checkRequirements` while a resource still points at the wrong VPC, or none at all.
- **Test without an account:** a narrow interface over the SDK client implemented twice, or an
  `httptest.Server` via the SDK's endpoint override. Put any live-account suite behind its own build
  tag and credentials, never in the default `go test ./...`.

---

## 11. Checklist before calling a plugin done

- [ ] Binary is named `infrata-plugin-<name>` and `Name()` returns `<name>`
- [ ] Every resource type is prefixed `<name>.`
- [ ] No attribute is called `prevent_destroy` or `retain`
- [ ] `Computed` on everything the cloud assigns; `ForceNew` on everything that cannot be changed in
      place; `Sensitive` on every secret
- [ ] No schema holds a function; every `Default` is a datum of its declared `Kind`
- [ ] `Create` and `Update` never return `(nil, nil)`
- [ ] Unknown configuration keys are refused, naming what is accepted
- [ ] `ClassifyError` defaults to `NotSafeToRetry` for anything unrecognised
- [ ] Nothing is ever written to stdout
- [ ] No credential or sensitive value is ever logged
- [ ] `go vet` and `gofmt -l .` are clean, and the suite passes with `-count=1`
- [ ] Every test has been sabotage-verified
- [ ] `plan` → `apply` → `plan` against a real project shows no changes on the second plan
- [ ] Removing a resource from configuration proposes destroying it, and applying that destroys it
- [ ] Mutating the cloud outside infrata makes the next `plan` propose the change (`refresh` records
      it into state; it does not print a diff)
- [ ] Removing an optional attribute from configuration converges: apply, then `plan` shows no
      changes
- [ ] An absolute path in configuration is used as written
- [ ] `plugin.yaml` is present, and its `name`/`version`/`protocol` agree with the code
- [ ] The release gate refuses a tag, manifest and binary that disagree
- [ ] `Version()` reports something like `0.0.0-dev` in an unstamped build, never the manifest's
      version
