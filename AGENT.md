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

Contract, in the infrata repository: `PLAN.md` §31.1 (the design and its reasoning),
`pkg/pluginproto` (the wire), `pkg/pluginsdk` (`Main`), `pkg/provider` (the interfaces), `pkg/schema`
(describing types), `pkg/value` (the value model).

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

---

## 2. The two interfaces

### `provider.Plugin` — the plugin itself

```go
type Plugin interface {
	Name() string
	Definitions() []*schema.ResourceDefinition
	New(instance string, config map[string]value.Value) (Provider, error)
}
```

- **`Definitions` must answer with no configuration at all.** Schemas are static: `hetzner.server`
  is described the same way whichever account it would be created in. This is what lets infrata
  compile a project before it knows any credentials, and it is the whole reason the interface is
  split in two.
- **`New` is called once per configured instance**, with that instance's own resolved configuration.
  One process serves many instances: two accounts of one cloud means one plugin process holding two
  configured clients. `instance` is the name the user gave it, and a plugin whose configuration is
  entirely optional still needs it to keep two instances apart.
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
| `Update` | the updated resource, **never `(nil, nil)`** | same |
| `Delete` | error only | deleting something already gone should succeed |
| `Discover` | everything that exists of the requested types | including resources infrata does not manage |
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

1. **Bookkeeping is never sent to you, so you cannot drop it.** You receive only the type, the
   provider ID and the attributes. Addresses, dependencies, lifecycle flags and timestamps are
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

- **`SafeToRetry`** — a throttle, a 5xx, a timeout on a read. Retrying cannot do harm.
- **`ConditionallyRetryable`** — it might have taken effect. Infrata is cautious with these.
- **`NotSafeToRetry`** — a validation failure, a permissions problem, or anything where a second
  attempt could create a second resource. **This is the right default when you are unsure.**

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
  that dies of a missing credential otherwise reports as `EOF`.
- **Never log a credential, a token, or the value of a `Sensitive` attribute.** stderr is shown to
  users and captured in CI logs. The redaction machinery protects values that travel through
  infrata; it cannot protect what you print yourself.
- Take credentials the way your cloud's own tooling does — the standard environment variables and
  config files — so that a user who can already use their cloud's CLI does not have to configure
  anything twice. Accept explicit configuration in `providers:` as an override, and remember it may
  come from a variable, so it can differ per environment.

---

## 7. The project directory

`New` receives the project directory when it needs to resolve a relative path from configuration —
a file the user named relative to their project rather than to whatever working directory the
plugin inherited. Most real plugins do not need it. If yours does, resolve against it rather than
against the process's `cwd`, which is not the project.

---

## 8. Testing a plugin

You do not need a cloud account, and you do not need a subprocess.

- **Test your `Provider` directly.** It is an ordinary Go interface; call its methods in a table
  test. This is where most of your coverage belongs.
- **Test the protocol path in process.** Infrata's `pluginhost.InProcess` runs the SDK on one end of
  an in-memory pipe and the host on the other, so every call is encoded, decoded and passed through
  the host's trust rules with no process involved. Use it to prove your plugin works through the
  real protocol — including that your schemas load, which catches the prefix and reserved-name
  rules.
- **Test the binary once.** One end-to-end test that builds the binary and runs infrata against it
  is enough to prove the packaging; everything else is faster and clearer at the two levels above.
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
- Report a real version from `Version()`, so a project can pin it:

  ```yaml
  plugins:
    hetzner: ">= 1.2.0, < 2.0.0"
  ```

  The constraint is checked against what your handshake reports. Comparison operators on
  `MAJOR.MINOR.PATCH`, comma meaning AND.
- **The protocol version is the compatibility contract, not the Go types you compiled against.** A
  plugin built against an older SDK keeps working for as long as its protocol version is supported.
  You do not have to rebuild for every infrata release.

---

## 10. Checklist before calling a plugin done

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
- [ ] Mutating the cloud outside infrata and running `refresh` reports the drift
