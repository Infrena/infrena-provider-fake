# Writing an infrena provider

This guide is for a Go developer who knows their cloud's API and has never used infrena. By the end
you should be able to build `infrena-plugin-<yourcloud>`, test it without a cloud account, and
release it.

[`AGENT.md`](../AGENT.md) is the short reference: what to do. This guide explains why, because the
rules only make sense once you know what the host does with your answers. Every claim about infrena
below was checked against infrena's source. Every excerpt is copied from this repository (a working
plugin, `infrena-plugin-fake`) or from infrena's own tree, with the `path:line` it came from. Paths
starting `pkg/`, `internal/` or `PLAN.md` are in the infrena repository. All other paths are in this
one.

**A note on the name.** Infrena was called Infrata until 2026-09-14, when it was renamed over a legal
name collision. The module path, the CLI, plugin binary names (`infrata-plugin-<name>` became
`infrena-plugin-<name>`), the `INFRATA_*` environment variables and the manifest's floor key all
changed. Engine releases up to v0.3.0 were published under the old names, and v0.4.0 will be the
first under the new one. This repository's plugin releases up to and including v0.2.0 are named
`infrata-plugin-fake` and pair only with infrata v0.3.0 and earlier. Where this guide cites a
pre-rename engine release or quotes its output, it keeps the name that release actually had.

**A note on syntax.** Configuration examples use infrena v0.5.0's grammar: a variable is written
`${var.x}`, and a bare first segment always names a resource, as in `${vpc.id}`. Projects written for
v0.4.0 and earlier wrote a variable as `${x}`, which v0.5.0 refuses with an error naming the `var.` fix.
Since v0.6.0 a resource may also be written whole, `${vpc}`, where the consuming attribute declares
which of its attributes it holds ([section 3](#references-an-attribute-that-holds-another-resources-identifier)).

Contents:

1. [What a plugin is](#1-what-a-plugin-is)
2. [The two interfaces, and what the host does with each result](#2-the-two-interfaces-and-what-the-host-does-with-each-result)
3. [Modelling a resource type](#3-modelling-a-resource-type)
4. [Requirements](#4-requirements)
5. [Errors and retries](#5-errors-and-retries)
6. [Cancellation](#6-cancellation)
7. [Testing without a cloud account](#7-testing-without-a-cloud-account)
8. [Credentials](#8-credentials)
9. [What the host enforces, so you don't](#9-what-the-host-enforces-so-you-dont)
10. [Versioning and releasing](#10-versioning-and-releasing)
11. [Depending on infrena today](#11-depending-on-infrena-today)
12. [The manifest, `plugin.yaml`](#12-the-manifest-pluginyaml)
13. [The release gate](#13-the-release-gate)
14. [A real cloud: AWS as the worked example](#14-a-real-cloud-aws-as-the-worked-example)

---

## 1. What a plugin is

A plugin is an ordinary Go executable. infrena starts it as a child process, and the two talk over
the child's stdin and stdout in newline-delimited JSON: one JSON object per line. You never write
that transport yourself. A plugin's whole `main` is one call:

```go
// cmd/infrena-plugin-fake/main.go:9
func main() { pluginsdk.Main(fake.NewPlugin()) }
```

`pluginsdk.Main` reads requests, runs each one in its own goroutine, calls your methods, and writes
the responses (`pkg/pluginsdk/serve.go:109-147`). Because every request gets a goroutine, one
process serves every operation infrena runs in parallel. One process also serves every configured
instance of the plugin: two accounts of your cloud means one process holding two configured
clients, told apart by a handle (`pkg/pluginproto/proto.go:134-140`).

### stdout is the protocol

Nothing but protocol messages may reach stdout. The host reads every line of it as a response. The
first line that does not parse fails the connection. Every call still waiting gets an error saying
the plugin "wrote something that is not a protocol message", and so does every call made after that
(`internal/pluginhost/client.go:114-119`, `client.go:141-156`).

That is why a stray print is so confusing to debug. The error lands on whichever operation happened
to be waiting when the bad line arrived, not on the code that printed it, so the report points
somewhere unrelated. The SDK guards the common case by pointing `os.Stdout` at stderr before your
code runs:

```go
// pkg/pluginsdk/serve.go:53-54
	stream := os.Stdout
	os.Stdout = os.Stderr
```

That catches `fmt.Println` in your code and in any library you call. It cannot catch a direct write
to file descriptor 1, which nothing in Go can intercept (`serve.go:50-52`). So don't write to stdout
at all, and don't rely on the guard.

### stderr is your log

Everything your plugin writes to stderr is kept by the host. Under `--verbose`, infrena prints each
line prefixed with the plugin's name, as `[fake] …` (`internal/pluginhost/connect.go:137-147`,
`internal/cli/context.go:107-112`). Without `--verbose` the lines are not shown, but the last 20 are
always kept (`connect.go:86`). If the plugin exits unexpectedly, the error message quotes them under
"Its last output was:" (`internal/pluginhost/errors.go:105-117`).

That tail is often the only explanation a user gets. A plugin that dies of a missing credential
otherwise shows up only as "the plugin stopped responding" — infrena replaces the bare `io.EOF` a
closed pipe leaves behind with that sentence (`internal/pluginhost/errors.go:100-117`), because "EOF"
is a Go sentinel, not something a user should have to know means "the plugin exited". It also means
anything you print can end up in an error message, even without `--verbose` (see
[Credentials](#8-credentials)).

### The cookie

The host launches a plugin with `INFRENA_PLUGIN_COOKIE` set to a fresh random value
(`internal/pluginhost/connect.go:62-68`). Without it, `pluginsdk.Main` prints what the binary is and
exits 2 (`pkg/pluginsdk/serve.go:34-40`):

```
$ ./infrena-plugin-fake
fake is an infrena provider plugin: it is run by infrena, not directly.
Put it where infrena looks for plugins and name it in your `providers:` block.
$ echo $?
2
```

The check exists because a protocol program started from a terminal would otherwise sit silently
waiting on stdin, which looks exactly like a hang. The cookie is not a security boundary. The host
only checks that it is present (`internal/pluginhost/cookie.go:9-12`). With the cookie set and an
empty stdin, the plugin writes its handshake line and exits 0:

```
$ INFRENA_PLUGIN_COOKIE=x ./infrena-plugin-fake </dev/null
{"protocol":1,"name":"fake","version":"0.0.0-dev"}
```

`scripts/release-check` uses exactly that to read a built binary's version (section 13).

### Where infrena finds the binary

The binary must be named `infrena-plugin-<name>`, plus `.exe` on Windows
(`internal/pluginhost/connect.go:150-155`). infrena searches these places in order and uses the
first match (`connect.go:159-202`, `internal/pluginhost/loader.go:185-195`):

1. `--plugin-dir`, then `INFRENA_PLUGIN_PATH`
2. `<project>/.infra/plugins/`
3. `~/.local/share/infrena/plugins/`
4. `$PATH`

While developing, `--plugin-dir ./bin` is the shortest loop. With `--verbose`, infrena prints which
path each plugin was loaded from (`loader.go:112-114`).

---

## 2. The two interfaces, and what the host does with each result

You implement two interfaces from `pkg/provider/provider.go`. `Plugin` is the plugin before any
configuration. `Provider` is one configured instance of it.

The split exists to break a cycle. To configure an instance you need its resolved configuration.
Resolving configuration needs variables, variables need a compile, and a compile needs the resource
schemas. Schemas need no configuration: a server type is described the same way whichever account
it would be created in. So infrena asks for schemas first, and configures instances later
(`pkg/provider/provider.go:79-90`).

In the host, both interfaces are wrapped by an adapter, `internal/pluginhost/adapter.go`. It sends
your methods only what they need, and then **rebuilds** your answer under its own rules before
anything else in infrena sees it. The adapter has no bypass (`adapter.go:119-123`). Each row below
says what the host does to the result.

| Method | What your plugin is sent | What the host does with the result |
| --- | --- | --- |
| `Plugin.Name()` | nothing | Must match the `plugin:` name that selected the binary. A mismatch is refused with a message naming both (`client.go:98-106`, `errors.go:55-69`). Every resource type must also be prefixed `<name>.` (`adapter.go:79-85`). |
| `Plugin.Definitions()` | nothing | Validated when the plugin loads (see [section 9](#9-what-the-host-enforces-so-you-dont)). Any failure refuses the whole plugin. |
| `Plugin.New(cfg)` | instance name, that instance's resolved configuration, project directory (`pkg/pluginproto/proto.go:128-132`) | An error is reported as "provider instance … could not be configured", pointing at the `providers:` entry (`internal/providers/prepare.go:302-309`). |
| `Version()` (optional) | nothing | Sent in the handshake. A plugin that doesn't implement it reports `0.0.0` (`serve.go:343-348`). |
| `Provider.Read` | type, address, provider ID, attributes | `(nil, nil)` means the resource is gone (`adapter.go:161-163`). Otherwise rebuilt, keeping the bookkeeping from the state infrena already held (`adapter.go:164`). |
| `Provider.Create` | type, address, desired attributes | `(nil, nil)` becomes an error saying the resource may exist untracked (`adapter.go:173-175`). Otherwise rebuilt, with the address taken from the desired resource (`adapter.go:185-187`). |
| `Provider.Update` | current and desired, each as type, address, provider ID, attributes | `(nil, nil)` becomes the same error (`adapter.go:200-202`). Otherwise rebuilt from current. |
| `Provider.Delete` | type, address, provider ID, attributes | Error only; the host rebuilds nothing. Make deleting something already gone succeed, as the fake does (`internal/fake/provider.go:222-236`). Otherwise a resource someone removed by hand turns the next destroy into an error about a resource that no longer exists. |
| `Provider.Discover` | the types wanted — nothing else. There is no region field on the request ([section 14](#regions-a-default-on-the-instance-overridden-per-resource) explains why) | Any type your plugin doesn't declare is skipped. Declared types have their attributes checked (`adapter.go:219-233`). |
| `Provider.Import` | the type, and the cloud's own ID | `(nil, nil)` becomes `no <type> with id "<id>"` (`adapter.go:245-247`). The address is assigned by infrena's `import` command, never by you (`internal/cli/import.go:166`). |
| `Provider.ClassifyError` | *called in your process* | See [section 5](#5-errors-and-retries). |

### What is never sent

A resource in infrena's state carries bookkeeping your cloud knows nothing about: its dependencies,
its lifecycle flags (`prevent_destroy`, `retain`, `ignore_changes`), and its creation and update
timestamps. **None of it is ever sent to a plugin.** The wire type has no fields for it
(`pkg/pluginproto/proto.go:151-172`).
Anything you set on a result is ignored, and the host re-attaches its own copy:

```go
// internal/pluginhost/adapter.go:303-318
	if carry != nil {
		// The bookkeeping the plugin was never sent and therefore cannot have
		// lost. Dependencies is the only source of destroy-ordering edges once a
		// resource leaves configuration (§14); Lifecycle is prevent_destroy.
		out.Address = carry.Address
		if carry.Provider != "" {
			out.Provider = carry.Provider
		}
		out.Dependencies = carry.Dependencies
		out.Lifecycle = carry.Lifecycle
		out.CreatedAt = carry.CreatedAt
		out.UpdatedAt = carry.UpdatedAt
		if out.ProviderID == "" {
			out.ProviderID = carry.ProviderID
		}
	}
```

The address is the one exception. It *is* sent, because it is a legitimate input: you might put it
in a tag, a remote name or an error message (`proto.go:161-169`). But the address on a *result* is
ignored and replaced as above.

So `current` is given to `Read` and `Update` for you to use: the ID to look the resource up, the
attributes to compare against. You don't need to copy anything back from it. The fake's results
carry only a type, an ID and attributes (`internal/fake/values.go:10-28`), and a test asserts that
(`internal/fake/provider_test.go:284-299`).

### Why `(nil, nil)` from `Create` is an error

`(nil, nil)` has an honest meaning for `Read`: nothing is there. For `Create` it could mean either
"nothing happened" or "something was created but I can't tell you what". If it was the second, a
real resource now exists with nothing in state pointing at it, so no later plan or destroy can find
it. The host refuses to guess, and says so to the user:

```go
// internal/pluginhost/adapter.go:268-274
	return fmt.Errorf(
		"%s reported no result from %s of %s, so infrena cannot record what now exists.\n"+
			"If the operation did take effect, that resource exists and is not in state: "+
			"check %s directly before re-running.\n"+
			"This is a defect in the plugin — a successful %s must report the resource it "+
			"acted on.",
		r.plugin.Name(), method, resourceType, r.plugin.Name(), method)
```

If your cloud's create call succeeds but you can't read back what it made, return an error that says
so and includes whatever ID you have.

**Recommendation: report what the API told you, not a guess.** If you cannot determine an attribute,
prefer an error over inventing a value, and prefer returning it unknown over inventing one — nothing
in the adapter checks that a returned value is known, and state legitimately stores an unknown
attribute a provider hasn't reported (`internal/state/testdata/state-v2.json`'s `endpoint`, asserted
still-unknown by `golden_test.go:136`). But an unknown you didn't have to return makes the next plan
noisier, since nothing can compare against it until a later `Read` fills it in — the fake's
`database.Create` returns `endpoint` as a known value for exactly this reason
(`internal/fake/provider.go:305`).

### `Discover` and `Import`

`Discover` answers "what exists?", and that includes resources infrena did not create, which is the
only reason discovery exists. The fake reports every resource in its cloud file, including ones
without an infrena address (`internal/fake/provider.go:238-271`). Filter by `req.Types` in your
plugin, using whatever list call your API has for a type. The host passes `req.Types` through and
does not filter your answer by it (`adapter.go:211-233`).

The host skips a discovered type your schema doesn't declare, so a plugin can know about resources
it doesn't model (`adapter.go:221-227`). An undeclared *attribute* on a declared type is still an
error, and it fails the whole discovery (`adapter.go:228-231`).

`Import` adopts one resource by the cloud's own ID. **Check that the ID really is the type you were
asked for.** `import hetzner.network 42`, where 42 is a server, must be refused. Otherwise state
records a server as a network, and the next plan proposes replacing real infrastructure to settle
the mismatch. The fake does this check (`internal/fake/provider.go:294-296`), tested by
`internal/fake/discover_test.go:171`.

### `Config`

`New` receives a `provider.Config` struct with three fields (`pkg/provider/provider.go:25-41`):

- `Instance` is the name the user gave this instance.
- `Values` is the instance's configuration, fully resolved. Every key in it came from the user.
- `ProjectDir` is the project directory, for resolving a relative path the user wrote.

It is a struct rather than a list of parameters so it can gain fields without breaking plugins
compiled by other people.

Use `ProjectDir`, not the process's working directory. Use an absolute path from configuration as
written: joining it onto `ProjectDir` silently moves it (`internal/fake/plugin.go:65-69`).

---

## 3. Modelling a resource type

A schema is plain data. It crosses a pipe as JSON, so it holds no functions
(`pkg/schema/attribute.go:6-9`). Here is the fake's database type:

```go
// internal/fake/definitions.go:25-55
		{
			Type:        "fake.database",
			Description: "A fake database. Requires a network.",
			Attributes: map[string]schema.Attribute{
				"engine": {Kind: value.KindString, Required: true, ForceNew: true, Description: "Database engine"},
				// A default is a datum, not a function: a schema has to survive a pipe.
				"size":     {Kind: value.KindInt, Description: "Storage in GB", Default: int64(10)},
				"password": {Kind: value.KindString, Sensitive: true, Description: "Administrator password"},
				// network holds a fake.network's identifier, so it says so: the declaration is
				// what lets configuration write `network: ${network}` and have infrena fill in
				// `.id`, and what makes `network: ${db.endpoint}` a compile error. The plugin
				// decides which attribute a reference means; infrena never guesses one.
				"network": {
					Kind:        value.KindString,
					Description: "Network this database sits in",
					References:  &schema.Reference{Type: "fake.network", Attribute: "id"},
				},
				// A composite attribute is deliberately present: without one,
				// nothing exercises the conversion between a typed Value and
				// the plain JSON the hand-editable cloud file must hold.
				"tags":     {Kind: value.KindMap, Description: "Free-form labels"},
				"endpoint": {Kind: value.KindString, Computed: true, Description: "Connection endpoint"},
			},
			Requirements: []schema.Requirement{{
				Name:        "network",
				Types:       []string{"fake.network"},
				Description: "A database must sit inside a network",
			}},
			Capabilities: schema.Capabilities{Create: true, Read: true, Update: true, Delete: true, Import: true},
			ImportID:     schema.ImportSpec{Description: "the database identifier, e.g. db-1"},
		},
```

`infrena explain fake.database` renders that schema. It needs no project file: infrena loads the
plugin named by the type's prefix.

```
$ infrena explain fake.database
fake.database
  A fake database. Requires a network.

Required:
  engine           string   Database engine  (replaces on change)

Optional:
  network          string   Network this database sits in  (refers to fake.network.id)
  password         string   Administrator password  (sensitive)
  size             integer  Storage in GB  (default: 10)
  tags             map      Free-form labels

Computed:
  endpoint         string   Connection endpoint

Requires:
  fake.network     A database must sit inside a network

Capabilities:
  create, read, update, delete, import

Import ID:
  the database identifier, e.g. db-1
```

The plan lines quoted below are real output from `README.md`, where the e2e suite applies this exact
project (`README.md:82-83`).

### `Computed`: the cloud assigns it

Mark an attribute `Computed` when your cloud decides its value: an ID, an IP address, an endpoint, a
generated password. Configuration may not set it, unless it is also `Optional`
([below](#optional-with-computed-the-cloud-picks-unless-configuration-says)). A user who tries gets
"is computed and cannot be set" (`internal/compiler/schema.go:192`). Before the resource exists, a plan shows the value as
unknown (`internal/planner/diff.go:103`):

```
      endpoint: (known after apply)
```

(`README.md:96`.) Another resource can still reference it: `database_url: ${db.endpoint}` plans as
`(known after apply)` too (`README.md:90`), and is resolved at apply time.

A computed attribute may not also be `Required` or have a `Default`, and `Optional` is refused
without `Computed`. `Definition.Validate` refuses all three (`pkg/schema/definition.go:94-101`).

### `ForceNew`: changing it replaces the resource

Mark an attribute `ForceNew` when your API cannot change it in place: a server's image, a
database's engine, a network's CIDR. A change to it plans as **replace** instead of **update**, and
the plan names the attribute that forced it (`internal/planner/render.go:94-97`). This is the
README's drift example: someone edited `engine` outside infrena.

```
  -/+ fake.database.db  (replacement forced by: engine)
    ⚠ This resource has 1 dependent resource.
      endpoint: "db-2.db.fake" -> (known after apply)
      engine: "mysql" -> "postgres"
```

(`README.md:211-214`.) Get `ForceNew` right. A user approves a plan largely on the difference
between *update* and *destroy and recreate*. If a field is missing `ForceNew` when it should have
it, the plan proposes an update your API then rejects. If a field has `ForceNew` when it shouldn't,
every edit to it proposes destroying real infrastructure.

### `Sensitive`: it is a secret

Mark passwords, tokens and private keys `Sensitive`. Their values are shown as `<sensitive>`
(`pkg/value/format.go:15`):

```
      password: <sensitive>
```

(`README.md:99`.) You do not need to mark individual values you return. The host forces the flag
from the schema onto every value it receives (`adapter.go:348-353`), and
`internal/fake/protocol_test.go:121-139` is a test of that: the fake marks nothing, and the host
still redacts. What the host cannot catch is a **schema** that forgets the flag, because the schema
is the only thing that knows which attributes are secret.

### `Default`: a datum

A default is a plain Go value of the attribute's declared `Kind`: `int64(10)`, `"gp3"`, `true`.
The compiler fills it in when configuration omits the attribute, and the plan marks where it came
from (`pkg/value/format.go:251-258`):

```
      size: 10 [default, from provider default]
```

(`README.md:100`.) A default of the wrong kind stops the plugin from loading. It fails as the schema
is encoded (`pkg/schema/wire.go:65-71`). Note `int64(10)`, not `10`: an untyped `10` is an `int`,
which `schema.DatumValue` accepts (`attribute.go:106-107`), but writing `int64` says what you mean.

**Why a datum and not a function?** The field used to be a function of environment, region,
account and project. A function cannot cross a pipe, and the one use it had was withdrawn
(`pkg/schema/attribute.go:63-76`). A value that really does vary by region or account is one of two
other things. Either it is the user's choice, in which case make it a variable they can see in
configuration, or your cloud decides it, in which case make it `Computed` and report it (and
`Optional` too, if configuration may also choose).

### An attribute `Read` reports but configuration omits

The planner diffs in both directions. An attribute configuration sets but `Read` doesn't report
plans a change, "not set on the resource" (`internal/planner/diff.go:108-114`). The converse holds
too: an attribute `Read` reports but configuration doesn't set plans a change with the reason
"removed from configuration", unless the schema marks it `Computed` or doesn't define it at all
(`diff.go:47-66`, `diffAttributes`). If that attribute is `ForceNew`, the change is a **replace**
(`forcesReplacement`, `diff.go:269`). A `Default`, or an instance's `defaults:`, is filled into
configuration before the diff (`internal/compiler/schema.go`, `applyDefaults` and
`applyInstanceDefaults`), so it prevents the change only when it equals what `Read` reports.

Adding `"tags": {}` by hand to the fake cloud file, for a database whose configuration sets no
tags, gives:

```
  ~ fake.database.db
      tags: {} -> (absent)

Plan: 0 to create, 1 to update, 0 to replace, 0 to destroy, 0 to forget.
```

So an attribute `Read` always reports must be `Required`, `Computed` (alone, or with `Optional` when
configuration may set it — next subsection), or have a default that matches what `Read` reports.
Omit an empty optional value from what `Read` returns; don't return it as `{}` or `""`. The "removed
from configuration" path is also what removes a real attribute a user deleted from configuration,
through [the `Update` contract](#the-update-contract).

### `Optional` with `Computed`: the cloud picks unless configuration says

Some attributes are both the user's and the cloud's: configuration *may* set them, and when it
doesn't, the cloud chooses. A subnet's availability zone is the classic case. Declare those
`Optional: true, Computed: true`.

**infrena:** (v0.3.0, `PLAN.md` §14.1, `pkg/schema/attribute.go:22-40`)

- **Set in configuration**, it is an ordinary attribute: diffed normally, and `ForceNew` applies.
- **Unset**, the value the provider reports is recorded and **never diffed** (`diffAttributes`,
  `internal/planner/diff.go:47-66`). It can never plan a change, and a `ForceNew` one can never plan a
  replacement, however the provider's value moves. The accepted cost: drift on an unset one is
  invisible to `plan`. `refresh` and `state show` still show it.
- **`Optional` without `Computed` is refused** by `Validate` (`pkg/schema/definition.go:96-99`):
  every attribute that isn't `Required` is already optional, so the flag alone would say nothing.
- **`explain`** lists these in their own group, "Optional, chosen by the provider if unset"
  (`internal/cli/explain.go:92-95`).
- **A plan** marks a provider-chosen value `[provider-chosen, not in configuration]` under
  `--verbose`, and always when the attribute is `ForceNew`, where a later explicit value would
  replace the resource (`renderAttributeName`, `internal/planner/render.go`). It does not say "no
  longer set in configuration", because state can't tell infrena whether configuration ever set it.
- **`import --generate`** writes the `ForceNew` ones, which are the resource's identity, and omits
  the updatable ones, which would pin every cloud default into the file (`internal/generator/generate.go`).

Your plugin does nothing special. On `Create`, report the value the API chose, as you would any
computed value. On an update where configuration leaves it unset, the plan carries the observed value
forward into the operation (`afterAttributes`, `internal/planner/diff.go:324-367`), so `Update` is
not asked to change it. An `Update` loop that keeps `Computed` attributes, like
[the fake's](#the-update-contract), keeps these too.

Reproduced 2026-09-14 against infrena v0.3.0, with a scratch copy of this plugin (not shipped) whose
`fake.database` gained a `zone` that the fake cloud fills with `zone-a` when configuration names
none. Declared `{Kind: value.KindString, ForceNew: true}`, the unedited basic project plans a
replacement straight after `apply`:

```
  -/+ fake.database.db  (replacement forced by: zone)
    ⚠ This resource has 1 dependent resource.
      endpoint: "db-2.db.fake" -> (known after apply)
      zone: "zone-a" -> (absent)
```

Declared `{Kind: value.KindString, Optional: true, Computed: true, ForceNew: true}`, the same
project plans `No changes. Configuration matches the observed state.` Writing `zone: zone-a` still
plans no changes, and `zone: zone-b` plans the replacement, `zone: "zone-a" -> "zone-b"`, which is
correct. Declared `Optional` without `Computed`, the plugin doesn't load:

```
Error: cannot describe "fake.database": the fake plugin sent an invalid schema: fake.database: attribute "zone" is Optional without Computed, which says nothing: every attribute that is not Required is already optional. Optional exists to pair with Computed (PLAN.md §14.1)
```

### `Aliases`: other spellings of one attribute

**infrena:** (v0.3.0, `PLAN.md` §14.1) `Aliases` lists alternative spellings configuration may use,
for example `Aliases: []string{"cidr", "cidr_block"}` on an attribute declared `CidrBlock`.

- **Matching is case-insensitive** across the canonical name and every alias, and folds case only:
  `cidr_block` and `cidrblock` are different spellings (`foldName`, `pkg/schema/alias.go`).
- **Names that fold together are refused at load.** `Validate` rejects two attributes, an attribute
  and an alias, or two aliases that are equal ignoring case (`checkSpellings`, `pkg/schema/alias.go`),
  so a collision is a plugin that won't start, not a runtime guess. **This binds plugins that declare
  no aliases too:** attributes `Name` and `name` on one type no longer load on an infrena v0.3.0 host,
  whatever protocol the plugin speaks, because the host validates every schema it receives
  (`adapter.go:72-74`).
- **The compiler resolves every spelling to the canonical name, once**, at its boundary
  (`canonicaliseAttributes`, `internal/compiler/schema.go`). Your plugin, state and the plan artifact
  only ever see the canonical name, so adding an alias in a later release changes no stored key.
  Configuration setting one attribute under two spellings is an error naming both.
- **Plans and `import --generate` display the first declared alias**, and `explain` lists every
  spelling (`Display` and `Spellings`, `pkg/schema/alias.go`). Put the spelling you want users to read
  first.
- **Aliases are schema, and cross the wire with it.** infrena holds no mapping of its own, so changing
  an alias needs a plugin release.

**Recommendation:** add an alias only for a spelling users genuinely reach for, such as the cloud
API's own name beside a friendlier one. A plugin generated from an API schema is the case the
feature was designed for; a hand-written plugin with names chosen once rarely needs one.

### `References`: an attribute that holds another resource's identifier

**infrena:** (v0.6.0, `PLAN.md` §14.3, `pkg/schema/attribute.go:14-35` and `:82-89`) An attribute may
declare `References: &schema.Reference{Type, Attribute}`: *this attribute holds that type's
attribute*. The fake declares one, on the database's `network`:

```go
// internal/fake/definitions.go:33-41
			// network holds a fake.network's identifier, so it says so: the declaration is
			// what lets configuration write `network: ${network}` and have infrena fill in
			// `.id`, and what makes `network: ${db.endpoint}` a compile error. The plugin
			// decides which attribute a reference means; infrena never guesses one.
			"network": {
				Kind:        value.KindString,
				Description: "Network this database sits in",
				References:  &schema.Reference{Type: "fake.network", Attribute: "id"},
			},
```

**The single rule: your plugin names the attribute, and infrena never guesses it.** Whether a
subnet's `VpcId` wants the VPC's id or its ARN is knowledge about your API, and it belongs to you. The
engine's whole part is to read the declaration and rewrite `${network}` into `${network.id}` at
compile time (`projectRefs`, `internal/compiler/bind.go`); the planner, executor, state and your
plugin only ever see the two-part reference. An attribute that declares nothing gets the behaviour it
had before v0.6.0. `${network}` there is a compile error naming the fix, **with no fallback to
"probably the id"**. Here it is for the fake's `database_url`, which declares nothing:

```
$ infrena plan dev
Error: ${db} passes a resource to an attribute that declares no reference
  at infra.yml:22:5

  `database_url` does not say which of "db"'s attributes it holds, so there is nothing to pick — write ${db.<attribute>} instead. The provider declares that, not infrena.

  Suggested action:
    Name the attribute you mean, as ${db.<attribute>}.
Error: configuration is not valid
```

What a declaration gives a user:

- **`${network}` is sugar, never a replacement.** `${network.id}` keeps working forever, so declaring
  a reference in a later release breaks no configuration that names the attribute.
- **Both spellings are type-checked.** Once `network` declares `fake.network`, writing `${app}` or
  `${app.url}` there is refused before anything runs (`checkReferredType`, `bind.go`):

  ```
  Error: network refers to fake.network, and "app" is fake.application
    at infra.yml:15:5

    ${app.id} reaches into a resource of the wrong type.

    Suggested action:
      Pass a fake.network, or name the attribute you mean on a resource of that type.
  Error: configuration is not valid
  ```

  So adding a declaration can turn a configuration that used to compile, one pointing the attribute
  at a resource of another type, into an error. That is the point, but say so in your release notes.
- **`explain` prints it:** `network  string  Network this database sits in  (refers to fake.network.id)`.
- **A module boundary carries no reference in either direction.** A module input declares a type, not
  a relationship, and an output has no consuming attribute, so `${network}` passed as a module input or
  published as a module output is refused with the `${network.<attribute>}` fix, whatever your plugin
  declares (`PLAN.md` §14.3; `refuseWholeResourceInput`/`refuseWholeResourceOutput`, `internal/modules`).

**Declare it only where the attribute holds an identifier.** The fake's `database_url` holds a
database's `endpoint`, a connection string rather than an identifier, so it declares nothing.
Declaring it would make `database_url: ${db}` silently mean "the endpoint", and would refuse a
`database_url` built from anything but a `fake.database`. `References` and `Requirements` are
different axes. A `Requirement` says "a database needs *some* network to exist" and names no
attribute; `References` says "*this* attribute holds a network's id". infrena does not derive one from
the other yet (`PLAN.md` §14.3, "not yet reconciled"), so keep declaring both.

**Refused at load** (`pkg/schema/definition.go`):

- **A dangling `Type` or `Attribute`:** `fake.database: attribute "network" refers to fake.network.ident, and fake.network has no attribute "ident"`.
  `Attribute` must be the target's **canonical** name, never an alias. This is `schema.ValidateAll`,
  which needs the whole set of definitions. infrena's registry runs it when the CLI loads your plugin
  (`checkDefinitions`, `internal/registry/registry.go`). **`plugintest.Open` does not:** the host
  adapter runs only each definition's own `Validate` (`internal/pluginhost/adapter.go:72`). So call
  `schema.ValidateAll(definitions())` in a unit test, as `internal/fake/definitions_test.go` does.
- **A `References` nested inside `Fields`.** Only a top-level attribute's declaration is ever
  consulted, so a nested one would do nothing, and is refused rather than ignored.

### `Fields`: a map attribute's known keys

**infrena:** (v0.6.0, `PLAN.md` §14.3, `pkg/schema/attribute.go:91-104`) A `KindMap` attribute may
declare `Fields map[string]schema.Attribute`, the keys the provider knows. Where it does, a path into
the map naming a key that isn't declared, such as `${lb.health_check.intervall}`, is a compile error
listing the keys that exist, instead of a clean plan that fails halfway through `apply`.

- **Nil means open, and that is a first-class answer.** Tags and labels take any key and always will,
  so declaring `Fields` for them would be a schema claiming to know a shape it doesn't. An open map is
  checked exactly as before: not at all, until apply. The fake's `tags` stays open for that reason, and
  so does every fake attribute: none is a map whose keys the plugin knows.
- **Declare it where the API fixes the keys**, for example a settings block with a documented set of
  fields. Each nested `Attribute` needs a `Kind`, and may itself declare `Fields` if it is a map.
- **`Fields` on an attribute whose `Kind` isn't `KindMap` is refused at load**, at every nesting
  level: `attribute "x" declares Fields but its Kind is not a map; Fields describes a map's known keys`.
- **`explain` lists a declared map's known keys.**

### Declaring either needs infrena v0.6.0 and protocol 3

`References` and `Fields` are new keys in the schema payload (`pkg/schema/wire.go:37-44`), and an
attribute decodes leniently. A host older than v0.6.0 would silently drop both, and your `${vpc}`
would fail as "declares no reference" with nothing explaining why. That is why v0.6.0 raised
`pluginproto.Version` to 3 (`pkg/pluginproto/proto.go:43-51`): an older host refuses a plugin
announcing 3 by name instead. A plugin that declares either must require infrena v0.6.0, write
`protocol: [3]` in `plugin.yaml`, and set `infrena: ">= 0.6.0"`
([section 12](#12-the-manifest-pluginyaml)). This repository does all three. A plugin that declares
neither and stays on an older SDK keeps announcing 2 or 1, which v0.6.0 still accepts
(`Supported = {3, 2, 1}`).

### The `Update` contract

`Update` receives the resource as it is (`current`) and as configuration wants it (`desired`). Make
the resource match `desired`. That includes **removing** anything `desired` no longer has.

Merging instead is the easy mistake. If a user deletes `tags:` from configuration, a merging `Update`
reports success but leaves the tags in place, and every later plan proposes removing them again,
forever. The one exception is computed attributes. infrena fills `desired` with the computed values
it has observed (`afterAttributes` in `internal/planner/diff.go`), but a computed value it has never
observed is simply absent. Absent there means "not known", not "remove it", so a
remove-everything-not-in-desired loop must keep computed attributes:

```go
// internal/fake/provider.go:201-215
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
```

Tested directly by `internal/fake/provider_test.go:249`, and end to end by the e2e subtest "removing
an optional attribute converges" (`e2e/e2e_test.go:206-214`).

---

## 4. Requirements

A `Requirement` says what a resource needs in order to exist, for example "a database must sit
inside a network". Declaring one gives your users missing-dependency detection. The compiler checks
it before any provider is called, and reports what is missing and how to fix it
(`internal/compiler/validate.go`, `checkRequirements`). A project that declares a `fake.database` and
no network (here with no `providers:` block, so the database's implicit instance is named after the
plugin, `"fake"`):

```
$ infrena plan dev
Error: "db" is missing required network
  at infra.yml:7:3

  A database must sit inside a network
  Satisfied by a resource of type: fake.network
  It must belong to the same provider instance, "fake": another instance is another account, and
  resources in one cannot reach the other.

  Suggested action:
    Add a resource of type fake.network to provider instance "fake".
Error: configuration is not valid
```

Without the requirement, that project would plan cleanly and the user would find out when your
cloud's API rejected the create, halfway through an apply. `explain` lists requirements under
`Requires:` (`internal/cli/explain.go:97-102`), shown in the output in section 3.

Know what a requirement checks: it is satisfied if **any** resource of a satisfying type exists in
the **same provider instance** — corrected 2026-09-13; it used to count types across the whole
configuration, so a database in one account was satisfied by a network in another, which two
instances (two accounts, §12.1) make meaningless. It is not a check that this resource references
that one, because a `Requirement` names no attribute to trace, and it is not region-aware: a subnet
requiring a VPC is satisfied by a VPC in any region of the same instance. The actual dependency, and
so the order resources are created in, still comes from the reference the user writes, such as
`network: ${network.id}`. `Optional: true` records a requirement without enforcing it.

**Recommendation: treat `Requirements` as a pre-flight hint, not the correctness mechanism.** It
checks that something of the right type exists in the same account, and nothing more — not state,
not a region, not a traced reference. Both limits are intended, not pending: infrena's stated fix for
either is the same one — letting a `Requirement` name the attribute it is satisfied by — and that is
deferred until a real cloud plugin says what it needs. Model the dependency as a `Required` reference
attribute too (`vpc_id: ${vpc.id}`), because the reference is what infrena actually validates end to
end; a plugin that relies on `Requirements` alone will pass validation with an under-specified or
wrong reference still in the configuration.

---

## 5. Errors and retries

When a call fails, infrena asks your plugin how dangerous it would be to try again. You classify.
infrena decides whether to retry and how long to wait (`pkg/provider/provider.go:52-60`). There are
three classes:

- **`SafeToRetry`**: the operation provably did not take effect. A throttle, or a 5xx your API
  guarantees was rejected before doing anything.
- **`ConditionallyRetryable`**: it might have taken effect. A timeout, or a connection dropped after
  the request was sent.
- **`NotSafeToRetry`**: a validation error, a permission error, anything that will fail the same way
  again, and anything you are unsure about.

### What the executor does with each

These are the executor's rules (`internal/executor/retry.go:105-120`), the only place it calls them
(`internal/executor/apply.go:501`), and the policy the CLI passes in (`internal/cli/apply.go:318-321`):

| Operation | `NotSafeToRetry` | `ConditionallyRetryable` | `SafeToRetry` |
| --- | --- | --- | --- |
| **create** (including the create half of a replace) | not retried | not retried | retried |
| **update** | not retried | retried | retried |
| **delete** (including the destroy half of a replace) | not retried | not retried | retried |
| **read** (plan, refresh) | not retried | not retried | not retried |
| **discover, import** | not retried | not retried | not retried |

"Retried" means up to three attempts in total. The wait between attempts starts at 500ms, doubles
each time, is capped at 10s, and is randomized ("full jitter") so failures don't all retry at once
(`internal/cli/apply.go:318-339`, `retry.go:127-150`). `apply` and `destroy` use the same policy
(`internal/cli/apply.go:396-411`). When the last attempt fails, the operation fails.

Some consequences worth knowing:

- **For create and delete, `ConditionallyRetryable` behaves exactly like `NotSafeToRetry`.** A
  create that timed out might have made a real resource, and retrying it could make a second one.
  A delete that timed out might have removed the object, and retrying could act on whatever now has
  that identity (`retry.go:79-88`).
- **For update, `ConditionallyRetryable` behaves exactly like `SafeToRetry`**, because making a
  resource match the same desired state twice is harmless.
- **Reads are never retried by the executor.** `retry.go` has a rule for `VerbRead`, but no caller
  uses it: only create, update and delete go through the retry loop (`apply.go:559-571`). Discovery
  and import don't go through it either. If a transient read failure should be retried, retry it
  inside your plugin.
- A classification value outside the three is never retried, for any operation (`retry.go:116-118`).

Classify honestly even where two classes behave the same today. The classification describes your
cloud. The retry rules belong to infrena, and a later version may treat the classes differently.

### Why `NotSafeToRetry` is the default

It is the zero value of `provider.Retryability` (`pkg/provider/provider.go:57`). The protocol treats
a missing classification as `NotSafeToRetry` (`pkg/pluginproto/proto.go:115-117`). The two ways to get
this wrong are not equally bad. Classify too cautiously, and a user re-runs `apply` after a transient
failure. Classify too permissively, and a retried create produces a duplicate resource that nothing
tracks. So return `NotSafeToRetry` for any error you don't specifically recognise:

```go
// internal/fake/provider.go:73-79
func (p *Provider) ClassifyError(err error) provider.Retryability {
	var injected *ErrInjected
	if errors.As(err, &injected) {
		return injected.Retryability.Classify()
	}
	return provider.NotSafeToRetry
}
```

### How classification crosses the pipe

`ClassifyError` takes an `error`, and a Go `error` can't be sent over a pipe. So the SDK calls your
`ClassifyError` inside your process, as it writes the failed response, and sends the answer with the
message (`pkg/pluginsdk/serve.go:287-293`). The host rebuilds a typed error carrying that answer, and
its own `ClassifyError` just reads it back (`internal/pluginhost/client.go:211-216`,
`adapter.go:142-147`). `internal/fake/protocol_test.go:141-165` checks that a classification
survives the trip.

Two things follow from how the SDK does this:

- **Make `ClassifyError` a pure function of the error.** The SDK asks *any one* of your configured
  instances to classify, not necessarily the one that failed (`serve.go:313-320`). Don't let the
  answer depend on per-instance state.
- **An error before any instance exists is `NotSafeToRetry`**, whatever your code would have said
  (`serve.go:319`).

### A failure on the host's side is always `NotSafeToRetry`

If the plugin crashes, or the pipe breaks, every call still waiting fails with `NotSafeToRetry`
(`client.go:150-154`). So does any error that did not come from the plugin at all
(`adapter.go:139-147`). This is exactly the case where nobody knows whether a create happened, so
the host never retries it. Because infrena writes state as each operation finishes, everything that
completed before the crash is still recorded (`client.go:136-140`).

### Write error messages that say what to do

A user reads your error in a failed apply summary, often in CI, with nothing else to go on. Say what
failed, what was expected, and what to do about it. `401` is not an error message. `the API rejected
the token (401): check HCLOUD_TOKEN is set and has write scope` is. The fake's import errors name the
ID, the file and the actual type (`internal/fake/provider.go:291-296`). Its configuration errors name
the key the user wrote and the one they probably meant (`internal/fake/plugin.go:84-96`).

---

## 6. Cancellation

When a user presses Ctrl-C, the host does not kill your plugin. It sends a `cancel` message for the
request, and the SDK cancels that request's `ctx` (`pkg/pluginsdk/serve.go:121-124`, `151-163`). Then
**the host keeps waiting for your answer**:

```go
// internal/pluginhost/client.go:192-195
	case <-ctx.Done():
		_ = c.write(pluginproto.Request{ID: id, Method: pluginproto.MethodCancel})
		// AND KEEP WAITING. The host always learns what the plugin actually did.
		resp := <-ch
```

If you answer with success, the host treats the operation as a success, even though the context was
cancelled. The resource really was created, and it must be recorded (`client.go:196-201`). Killing a
plugin halfway through a create is how a real resource ends up orphaned, which is what the
"never `(nil, nil)`" rule exists to prevent.

The executor goes further. It runs create, update and delete on a context that apply's cancellation
doesn't reach (`internal/executor/context.go:41-43`, used at `internal/executor/dispatch.go:68`,
`83`, `96`). So during `apply` and `destroy` today, a provider call in flight is not cancelled at
all: Ctrl-C lets it finish (`client.go:202-206`). Write your plugin for the general contract anyway,
because another caller may cancel:

- **Check `ctx` before a mutating call.** If you're cancelled before the create request is sent,
  return `ctx.Err()`. Nothing happened, and nothing is lost.
- **Check `ctx` between pages of a paginated read.** Abandoning a read loses nothing.
- **Never let cancellation stop you reporting a mutation that already happened.** Once the create
  request is sent, finish reading the response and return the resource. Don't pass a cancelled
  `ctx` into the call that reads the result back and then return that error.

The fake's simulated latency shows the shape. `ctx` is checked before anything else — even before
`latency_ms` is consulted, because an already-cancelled call must not start whether or not there is
a delay to abandon it in — and the delay itself honours `ctx` too, and runs **before** the mutation,
so cancelling abandons only work that hasn't started:

```go
// internal/fake/provider.go:113-131
func (p *Provider) delay(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

`internal/fake/inject_test.go:323-347` checks that a create cancelled during the delay returns
`context.DeadlineExceeded` and leaves the cloud empty.

---

## 7. Testing without a cloud account

You need neither a cloud account nor a subprocess for most tests. This repository tests at three
layers. Put most of your tests in the first.

### Layer 1: your `Provider`, against a fake cloud

`Provider` is an ordinary Go interface, so call its methods directly. The trick is what sits behind
it. **Fake the cloud, not your own code.** Here the cloud is a JSON file, so a test changes the
cloud by editing the file, exactly as a person simulating drift would:

```go
// internal/fake/provider_test.go:56-63
	c, err := LoadCloud(path)
	if err != nil {
		t.Fatalf("LoadCloud: %v", err)
	}
	c.Resources[st.ProviderID].Attributes["engine"] = "mysql"
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
```

For a real cloud, the equivalent is an `httptest.Server` that speaks enough of your API, or an
interface over your API client with a test implementation. Don't mock your plugin's own methods: a
test that mocks the thing under test proves nothing.

Examples in this repository: `internal/fake/provider_test.go` (CRUD, drift, removing an attribute),
`internal/fake/discover_test.go` (discovery, import, wrong-type import),
`internal/fake/inject_test.go` (failure classification, concurrency, latency, cancellation), and
`internal/fake/plugin_test.go` (configuration: unknown keys, path resolution, instances).

### Layer 2: the protocol and the trust rules, with `pkg/plugintest`

`plugintest.Open` runs your plugin on one end of an in-memory pipe and infrena's real host on the
other. It adds no second implementation of either (`pkg/plugintest/plugintest.go:17-21`). Every
call is encoded, decoded and put through the host's rules, with no process started:

```go
// internal/fake/protocol_test.go:17-26
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
```

`Open` fails if the host would refuse your schemas: a type outside your prefix, a reserved attribute
name, a default of the wrong kind (`plugintest.go:40-46`). That makes `Open` alone worth a test.
`Configure` returns the host's own adapter, so the results you get back have been through every rule
in [section 9](#9-what-the-host-enforces-so-you-dont) (`plugintest.go:58-63`).
`host.Definitions()` returns your schemas after their JSON round trip, which is where a default that
doesn't survive encoding shows up (`internal/fake/protocol_test.go:31-50`).

The in-memory pipe is `internal/pluginhost.InProcess` (`internal/pluginhost/connect.go:28-58`).
infrena's own in-process test suites use it too, to serve a fake provider test double without a
binary (`internal/cli/main_test.go`, `TestMain`). A shipped infrena build serves nothing that way: it
carries no provider at all (`internal/cli/context.go:64-83`, `builtinsFor`). `pkg/plugintest` exists
because a package under `internal/` can't be imported by another module (`plugintest.go:22-24`).

**Know which layer to assert at.** Don't write a layer-1 test that asserts your `Provider` marks
sensitive values or carries bookkeeping forward. It shouldn't do either, so that test would pin
behaviour you are supposed to leave out. Assert instead, through `plugintest`, that the *host* does
it. The fake has both halves: `internal/fake/provider_test.go:284-299` asserts the plugin leaves
bookkeeping **unset**, and `internal/fake/protocol_test.go:89-117` and `:121-139` assert the host
re-attaches bookkeeping and redacts a discovered password.

### Layer 3: the binary, once, behind a build tag

One suite builds real binaries and runs a real `infrena` against your plugin. That proves the
packaging: the binary name, the search path, the handshake, and real output. `e2e/e2e_test.go` is
behind `//go:build e2e` (`e2e/e2e_test.go:1`) because it builds the `infrena` CLI from source, which is
slow. Without the tag, `go test ./...` never builds it. Run the suite with
`go test -tags e2e -count=1 ./e2e/`. It looks for the infrena source in `INFRENA_SRC`, or next to this
repository by default, and skips if the source isn't there (`e2e/e2e_test.go:37-45`, in `TestMain`).

Its main test, `TestTheWorkflow` (`e2e/e2e_test.go:172`), walks the whole workflow against one project: explain,
plan, apply (with a clean re-plan), a hand edit planning as a forced replacement and its repair, a
removed optional attribute converging, an injected failure failing the apply once, removing a
resource destroying it, discover plus import adopting what infrena did not create, and finally
destroy emptying the cloud. Its projects are
`e2e/testdata/basic/infra.yml` and `e2e/testdata/instances/infra.yml`. The README quotes the first
byte for byte, and `internal/fake/readme_test.go` fails if they drift apart.

Keep this layer small. Each case costs a full process launch, and a failure here tells you less about
where the bug is than the same failure at layer 1 or 2.

The dependency also runs the other way for this one plugin. infrena's own integration suite builds
`infrena-plugin-fake` from a sibling checkout of this repository and runs infrena against the binary,
because infrena no longer ships any provider of its own (`tests/integration/plugin_test.go:15-26`,
`buildFakePlugin`). Your plugin has no such arrangement. Its binary-level suite is the only proof that
your packaging works.

### Sabotage every test

A passing test is not evidence until you have seen it fail. After writing a test, break the code it
covers and check the test catches it. The break must leave the code **compiling** and change its
**behaviour**: delete the removal loop in `Update`, make `ClassifyError` always return `SafeToRetry`,
drop the type check in `Import`. A break that stops compilation tells you nothing, because every test
"fails".

Also check the fixture. If the fixture would satisfy the assertion whether or not the code works,
the test checks nothing. A test that creates one resource and then asserts "discovery finds a
resource" passes even if discovery ignores the type filter.

Always run with `-count=1`. Go caches test results, and a cached pass hides a fixture you just edited.

---

## 8. Credentials

Most of this section is recommendation, not host behaviour. infrena has no credential mechanism of
its own. Your plugin gets its configuration and its environment, and nothing else.

**Take credentials the way your cloud's own tooling does**: its standard environment variables and
config files. The plugin process inherits infrena's environment (`internal/pluginhost/connect.go:67-68`).
A user who can already use your cloud's CLI shouldn't have to configure anything twice.

**Accept explicit configuration in `providers:` as an override**, for users with more than one
account. Remember that a value there reaches `New` *resolved* (`pkg/provider/provider.go:33-35`). It
may come from a variable, so it can differ between environments, and the same `providers:` entry
may mean a different account in `staging` and `prod`. Resolve credentials inside `New`, not at
package init. (This is infrena's current design — `plan`, `apply`, `refresh`, `destroy` and
`import <env>` all resolve variables in `providers:` against the environment named on the command
line. `discover` alone takes no environment, so it resolves only what doesn't need one and refuses
anything else by name: see
[section 14](#regions-a-default-on-the-instance-overridden-per-resource).)

**Refuse configuration keys you don't recognise, and name both the key written and the keys you
accept.** If a misspelled `tokne:` is silently ignored, the instance falls back to the environment's
default credentials. It then quietly works against someone else's account, and the first sign is a
plan proposing to destroy resources you don't own. The fake refuses unknown keys:

```go
// internal/fake/plugin.go:84-96
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

and the user sees it against their `providers:` entry:

```
$ infrena plan dev
Error: provider instance "main" could not be configured
  at infra.yml:7:5

  unknown configuration "clowd"; the fake provider accepts only `cloud`

  Suggested action:
    Correct the `providers:` entry for "main".
Error: configuration is not valid
```

**Never log a credential, a token, or a `Sensitive` value.** stderr is not private. Under `--verbose`
it goes to the user's terminal, and so into CI logs (`internal/cli/context.go:107-112`). Even without
`--verbose`, its last 20 lines are quoted in the error when your plugin exits unexpectedly
(`internal/pluginhost/errors.go:105-117`). A debug line printing the request headers just before a
panic puts the token in the failure message. infrena's redaction works on values that travel through
infrena marked sensitive (`pkg/value/format.go:15`). It cannot redact text your plugin prints itself.
The same goes for error messages: don't format a credential into an `error`, because its text is what
the user sees.

---

## 9. What the host enforces, so you don't

Several guarantees used to be rules in a doc comment that every provider had to remember. A binary
someone else built can't be held to a comment, so infrena's host enforces them for every plugin
(`PLAN.md` §31.1, "What the engine stops trusting a plugin with").

**Do not reimplement any of these.** It isn't only wasted effort. A plugin that also does the host's
job has tests that keep passing when the host's check is broken, and so it hides the host bug it was
supposed to guard against.

1. **Bookkeeping is never sent, so it can't be dropped.** Dependencies, lifecycle and timestamps never
   reach the plugin, and are re-attached to results (`adapter.go:303-318`). *Prevents:* losing
   `Lifecycle`, which makes a `prevent_destroy` guard vanish with no error, and losing
   `Dependencies`, which is the only destroy-ordering information once a resource has left
   configuration.

   The same holds for `lifecycle: ignore_changes: [...]` (infrena v0.3.0, `PLAN.md` §14.2). A user
   lists attributes something else owns, such as a task revision a CI pipeline sets on every deploy,
   and the planner stops proposing to revert them: state keeps the observed value, a create uses
   configuration's, and a replacement resets them and the plan says so. It is planner work on
   `Lifecycle.IgnoreChanges` (`pkg/resource/resource.go`), and `Lifecycle` never crosses to a plugin,
   so there is nothing for you to implement. Nor is it a reserved attribute name
   (`internal/registry/registry.go:291` still lists only `prevent_destroy` and `retain`): those two
   are reserved because an instance's `defaults:` accepts them for every resource, and it does not
   accept `ignore_changes` (`lifecycleFor`, `internal/compiler/bind.go`).
2. **Sensitivity is forced from the schema.** Every value for a `Sensitive` attribute is marked,
   whatever the plugin sent (`adapter.go:348-353`). *Prevents:* a plugin that forgets the flag
   putting a password into a plan, a report, or configuration generated by `import --generate`.
3. **Provenance belongs to the host.** Every returned value is stamped as coming from the provider
   (`adapter.go:354-357`). *Prevents:* a plugin claiming a value came from the user's configuration,
   which would make a plan credit the wrong file.
4. **`(nil, nil)` from `Create` or `Update` becomes an error** saying the resource may exist
   untracked (`adapter.go:173-175`, `200-202`, `267-275`). *Prevents:* a real resource being recorded
   as "nothing happened" and orphaned.
5. **An attribute the schema doesn't declare is refused**, with an error naming it and listing what
   is declared (`adapter.go:338-346`). *Prevents:* a typo in a returned key being stored in state and
   showing up in a plan as a change nobody can explain.
6. **Schemas are validated when the plugin loads.** Each is checked by `Definition.Validate`
   (`adapter.go:72-74`, `pkg/schema/definition.go:79-120`), against the type-prefix rule
   (`adapter.go:79-85`), and against the reserved attribute names `prevent_destroy` and `retain`
   (`adapter.go:86-96`, `internal/registry/registry.go:291`). Since v0.3.0 `Validate` also refuses
   `Optional` without `Computed`, and any two attribute names or aliases equal ignoring case
   ([section 3](#aliases-other-spellings-of-one-attribute)). The registry then applies its own
   checks to the loaded plugin (`internal/registry/registry.go:75-95`): no type in the `module.`
   namespace, and no type already claimed by another plugin (`registry.go:246-265`). A default of the
   wrong kind fails when the schema is encoded (`pkg/schema/wire.go:65-71`). *Prevents:* two plugins
   claiming one type, with the winner depending on load order; a `prevent_destroy` attribute that
   collides with the lifecycle option of the same name; and a malformed schema surfacing halfway
   through a plan instead of at startup.
7. **The handshake is checked.** An unsupported protocol version is refused with advice on which side
   to upgrade (`client.go:90-97`, `errors.go:23-46`). A name that doesn't match the requested plugin
   is refused (`client.go:100-106`). *Prevents:* a renamed or mis-copied binary serving the wrong
   schemas.
8. **Error classification travels with the error, and a host-side failure is `NotSafeToRetry`**
   (`client.go:150-154`, `adapter.go:142-147`). *Prevents:* a crash or broken pipe during a create
   being retried into a duplicate resource.
9. **The retry and backoff policy belongs to infrena** (`internal/executor/retry.go`). *Prevents:*
   every plugin inventing its own idea of when a create is safe to repeat. Classify, and let the
   executor decide. (A retry *inside* your plugin for a transient read failure is fine, since the
   executor never retries reads; see section 5.)

---

## 10. Versioning and releasing

### Reporting a version

Implement `Version() string` on your `Plugin`. The SDK sends it in the handshake. A plugin that
doesn't implement it reports `0.0.0` (`pkg/pluginsdk/serve.go:338-348`).

Don't hard-code the release number. Default the variable to something that is obviously not a
release, and stamp the real version at build time with `-ldflags -X`:

```go
// internal/fake/plugin.go:21
var Version = "0.0.0-dev"
```

```bash
# scripts/build-release:37-39 (inside the per-platform loop)
    go build -trimpath \
      -ldflags "-X github.com/infrena/infrena-provider-fake/internal/fake.Version=${version}" \
      -o "$work/$stem/$name" ./cmd/infrena-plugin-fake
```

Section 13 explains why the default must never equal the version in `plugin.yaml`.

### Building and naming

A plugin is a plain Go binary, so building for another platform is only `GOOS` and `GOARCH`.
`CGO_ENABLED=0` gives a static binary that doesn't depend on the target's libc
(`scripts/build-release:34-36`). The executable inside the archive must be named
`infrena-plugin-<name>`, with `.exe` for Windows (`scripts/build-release:25-26`), because that is the
filename infrena searches for (`internal/pluginhost/connect.go:150-155`). Archive naming is covered in
section 13.

### How a project pins your version

```yaml
plugins:
  hetzner: ">= 1.2.0, < 2.0.0"
```

The constraint syntax is `pkg/semver`'s. It is intentionally small:

- **Comparison operators** `>=`, `<=`, `!=`, `==`, `>`, `<`, `=` on `MAJOR.MINOR.PATCH`
  (`pkg/semver/semver.go:119`).
- **A comma means AND.** Every term must hold (`semver.go:158-166`).
- **A bare version pins exactly**: `"1.2.0"` means `== 1.2.0`, not `>= 1.2.0` (`semver.go:123-125`).
- **Missing parts are zero**: `0.4` means `0.4.0` (`semver.go:42-45`).
- **Pre-release suffixes are ignored when comparing**, so `0.0.0-dev` compares as `0.0.0`
  (`semver.go:81-86`).

The constraint is checked against the version in the **handshake**, by the loader, so every command
that loads plugins applies it (`internal/pluginhost/loader.go:37-42`, `141-161`). An unstamped
development build fails any constraint above `0.0.0`:

```
Error: the fake plugin does not satisfy this project's `plugins` constraint, and it is needed by fake.database
  at infra.yml:4:3

  the fake plugin is version 0.0.0-dev, which does not satisfy >= 1.0.0
    loaded from: …/bin/infrena-plugin-fake

  Suggested action:
    Install a version matching >= 1.0.0, or widen the constraint once you have confirmed this one works.
```

Only a plugin reporting exactly `0.0.0` gets the separate message "does not report a version"
(`loader.go:157`, `errors.go:145-151`). Two more rules, both from `PLAN.md` §31.1:

- **A constraint naming a plugin the project doesn't use is an error**, because it would pin nothing
  (`internal/providers/prepare.go:169-194`).
- **There is one version per plugin, not per instance.** `plugins:` is keyed by plugin because every
  instance of a plugin shares one process (`PLAN.md` §31.1).

Don't confuse this with a project's `infrena:` floor, the constraint on infrena itself. That check
*exempts* development builds of infrena (`internal/compiler/compile.go:231-269`). `plugins:` does not
exempt development builds of your plugin, because for the user it is a third-party binary they chose
to install, and they can act on the complaint (`loader.go:136-140`).

### The protocol version is the compatibility contract

What must stay compatible between infrena and your plugin is the **wire protocol**, not the Go types
you compiled against (`pkg/pluginproto/proto.go:9-12`). The host accepts a *set* of protocol versions
(`proto.go:25-59`). A plugin built against an older SDK keeps working as long as its protocol version
is in that set, so you don't have to rebuild for every infrena release (`PLAN.md` §61.3). Under
infrena's own versioning rules, a minor release may add a protocol version but must keep the previous
one, and only a major release may drop one (`PLAN.md` §61.1). If the set no longer includes your
version, the user gets an error naming your plugin, its path, both sides' versions, and which one to
upgrade (`internal/pluginhost/errors.go:23-46`).

**infrena:** v0.3.0 raised `pluginproto.Version` to 2 and made `Supported` `{2, 1}` (`proto.go:25-59`).
The messages kept their shape. The schema payload gained `optional` and `aliases`, and because an
attribute decodes leniently, an older host would silently drop both, so a plugin relying on them must
be refused by that host rather than half-work. Every plugin built against v0.3.0 announces 2, whether
or not it uses either field; one built against v0.2.0 still announces 1 and still loads. That bump is
what changes your manifest's `protocol` ([section 12](#protocol-what-this-releases-binary-speaks)).

**infrena:** v0.6.0 raised `pluginproto.Version` to 3 and made `Supported` `{3, 2, 1}`
(`proto.go:43-59`), for the same reason: the schema payload gained `references` and `fields`
([section 3](#declaring-either-needs-infrena-v060-and-protocol-3)). Every plugin built against v0.6.0
announces 3.

Because of that, `pkg/pluginproto` "changes additively, and any removal bumps `protocol`" (`PLAN.md`
§31.1, "Handshake and version") — a new optional field on the wire is not a protocol bump. `pkg/value`
picked up exactly such a field: an unknown value may now carry the expression that will produce it,
added `omitempty` with no version change (infrena commit `ca9db09`). Decode and encode values through
`pkg/value` itself, never a hand-rolled or strict decoder — one that rejects a key it doesn't
recognise breaks on the next such change, even though nothing else about the protocol moved.

---

## 11. Depending on infrena today

`github.com/infrena/infrena` is a private repository, and it stays private until infrena is feature
complete (`PLAN.md` §31.1, "The repository stays PRIVATE until feature complete"). Its releases are
real module versions, but fetching one needs credentials. The setup this repository uses is two
directives: a `require` naming the infrena **release** your plugin supports, and a `replace` pointing
at a checkout of infrena next to your plugin, for local work:

```
// go.mod
require github.com/infrena/infrena v0.6.0

replace github.com/infrena/infrena => ../infrena
```

**v0.4.0 is the oldest release you can require.** Tags `v0.1.0` to `v0.3.0` were cut before the
rename and declare the old module path, `github.com/infrata/infrata`, so none of them satisfies a
require on `github.com/infrena/infrena`.

`../infrena` is the directory `git clone` of infrena creates, so a fresh clone of both repositories
side by side builds with no extra setup. Nothing else is needed from infrena. The SDK and protocol use
only the standard library, so a plugin gains no third-party dependency from them (`PLAN.md` §31.1).

### A `replace` builds against a working tree, not a version

`replace => ../infrena` compiles **whatever is on disk** in that checkout, committed or not. A green
suite therefore proves your plugin works against *your* infrena working tree, which may hold
uncommitted edits or a stale branch. It does not prove the plugin works against committed infrena.
This bit this repository once. Its build saw a stale `internal/semver` that infrena had already
moved, because the checkout on disk wasn't what was committed (`PLAN.md` §31.1, "The repository stays
PRIVATE until feature complete").

So CI must not use the `replace`. This repository's gating CI job drops it and builds against the
infrena release `go.mod` requires, fetched as a module (`.github/workflows/ci.yml`, job `tag`). Locally,
`git -C ../infrena status`, and check out the required tag, before you trust a result.

**Recommendation:** keep a second, advisory CI job that does use the `replace`, against a fresh
checkout of infrena's `main`. The two answer different questions: the tag job asks "does this plugin
work with the infrena it declares?", the `main` job asks "has infrena `main` broken us?", which is an
early warning about the next release. Only the first should gate a release. This repository marks
the second `continue-on-error: true` (`ci.yml:106`).

### CI needs credentials for infrena

Because infrena is private, CI needs a token both to check infrena out (for the e2e host) and for Go
to fetch the module. This repository uses one repository secret, `INFRENA_CHECKOUT_TOKEN`. Make it a
fine-grained personal access token scoped to **Contents: read-only** on `infrena/infrena` and nothing
else.

Every out-of-tree plugin needs the same four steps while infrena is private. This repository puts
them in `scripts/ci-use-infrena-tag use`, called from both `ci.yml` and `release.yml`:

1. **`GOPRIVATE=github.com/infrena/*`** in the job's environment. Go then fetches with git directly
   and skips the public module proxy and checksum database, neither of which can see a private
   repository.
2. **Git credentials from the token**, passed through `env` and never echoed:

   ```bash
   git config --global url."https://x-access-token:${INFRENA_TOKEN}@github.com/infrena/".insteadOf "https://github.com/infrena/"
   ```

3. **Drop the `replace`**: `go mod edit -dropreplace=github.com/infrena/infrena`. Then build and test
   under the default `-mod=readonly`.
4. **Check what resolved**: `go list -m -f '{{.Version}}{{with .Replace}} => {{.Path}}{{end}}'
   github.com/infrena/infrena` must print exactly the required tag.

Three things that are easy to get wrong:

- **Commit infrena's hashes to `go.sum`.** With the `replace` dropped, `-mod=readonly` needs
  `github.com/infrena/infrena v0.4.0 h1:…` and its `/go.mod h1:…` line. Don't have CI run `go mod
  tidy` or `go mod download` to write them: a checksum CI generated for itself verifies nothing, and
  because `GOPRIVATE` bypasses the checksum database, the committed hash is the only thing that
  would notice the tag being moved. Generate them locally, once, with the `replace` dropped. `go mod
  tidy` with the `replace` present **removes** them, so add a unit test that fails when they are
  missing and says how to restore them (`TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds` in
  `scripts/scripts_test.go`; the restore is `scripts/ci-use-infrena-tag sum`, which runs `go mod tidy`
  on a scratch copy of `go.mod` without the `replace`). A no-argument `go mod download` records only
  the `/go.mod` hashes, not enough to build.
- **Read the version from `go.mod`, not from `go list -m`.** `go mod edit -json` reads the file alone,
  so it works before any infrena checkout exists; `go list -m` loads the module graph, which with the
  `replace` present needs `../infrena`. Use that one read for both the module and the ref of the e2e
  host's checkout (`ci.yml:61-63` and `ci.yml:71`), so the two can never disagree.
- **A reusable workflow gets no secrets unless its caller passes them.** `release.yml` calls `ci.yml`
  with `secrets: inherit` (`release.yml:23`). Without it the token is empty and every fetch fails;
  `scripts/ci-use-infrena-tag` refuses an empty token under GitHub Actions and says so.

Check the e2e host out somewhere other than `../infrena` (this repository uses `infrena-host`, with
`INFRENA_SRC` pointing at it). A leftover `replace` would otherwise resolve to that checkout quietly,
and the build would never prove it can fetch the module.

### Keep your module path outside infrena's

**Your plugin's module path must not be under `github.com/infrena/infrena/`.** The official AWS
plugin, for example, is `github.com/infrena/infrena-provider-aws`, a separate path, not
`github.com/infrena/infrena/providers/aws`.

The reason is Go's `internal/` rule, which is checked by **import path, not by module**. infrena
tested this on a scratch copy of its own repository (`PLAN.md` §31.1, "Where the code lives"):

- A nested module named `github.com/infrena/infrena/providers/awsprobe`, with `replace => ../..`,
  **compiled** while importing `github.com/infrena/infrena/internal/pluginhost`.
- The identical file in a module named `example.com/outsideprobe` failed with `use of internal package
  github.com/infrena/infrena/internal/pluginhost not allowed`.

A plugin under infrena's path can therefore quietly depend on engine internals that no other plugin
can reach, and the compiler never says so. Outside that path, if it compiles, every dependency is one
any plugin author has. That is also why `pkg/plugintest` exists: `internal/pluginhost` is unreachable
from a correctly named plugin (`pkg/plugintest/plugintest.go:22-24`).

Your module's own `go` directive must be at least infrena's: `go 1.27.0` as of 2026-09-13 (infrena's
`go.mod:15`, and this repository's `go.mod:3`). If it's lower, the build fails with Go's ordinary
"requires go >= …" error, which names the module whose requirement it is. If that module is
infrena, raise your `go` line to match infrena's `go.mod`.

---

## 12. The manifest, `plugin.yaml`

Every plugin repository ships a `plugin.yaml` at its root that says what the plugin is and what it
works with (`PLAN.md` §31.2). This repository's:

```yaml
# plugin.yaml
# plugin.yaml: what this plugin is, and what it works with. infrena PLAN.md §31.2.
# Read at a release TAG, never at the default branch, which describes unreleased code.
# manifest: 2 since the Infrata -> Infrena rename, which renamed the floor key `infrata:` to
# `infrena:`. Releases up to v0.2.0 were tagged with `manifest: 1` and stay readable as they are.
manifest: 2
name: fake
version: 0.2.0
# The protocol THIS RELEASE'S binary speaks: for an SDK-built plugin, exactly one version, the
# pluginproto.Version of the infrena go.mod requires. It changes in the same commit as that require
# (internal/fake/manifest_test.go and scripts/release-check refuse a mismatch), never goes stale,
# and a later host protocol bump forces no re-release: the host keeps accepting older versions.
protocol: [3]
platforms: [linux/amd64, linux/arm64, linux/arm, linux/386, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64]
description: A fake provider for testing infrena without a cloud account.
# The oldest infrena release this plugin is tested with: 0.6.0, the release go.mod requires, so the
# floor is the release CI builds and runs the e2e suite with. 0.6.0 added provider-declared
# references (fake.database's network accepts ${network}) and protocol 3, which is what this binary
# speaks; 0.5.0 changed the configuration grammar (a variable is ${var.x}). Releases before 0.4.0
# are infrata, with a different module path, CLI and plugin binary name.
# Nothing refuses a mismatched host at runtime yet; infrena checks this at install (PLAN.md §31.3),
# which is designed but not built.
infrena: ">= 0.6.0"
source: https://github.com/infrena/infrena-provider-fake
```

**Write `manifest: 2`.** Format version 2 exists because of the rename: it spells the floor key
`infrena:` where version 1 spelled it `infrata:`. That is a renamed key, not an added one, so the
format version moved. `pkg/pluginmanifest` refuses `infrata:` in a version 2 manifest and `infrena:`
in a version 1 one, each with a message naming the mistake (`checkFloorSpelling`,
`pkg/pluginmanifest/manifest.go:161`). The alternative is worse: an unrecognised floor would read as
absent, and absent means unconstrained. A release tagged with `manifest: 1` keeps being read as
version 1, because the manifest is read at the tag.

infrena can parse and check a manifest: `pkg/pluginmanifest` has `Parse`, `Validate`,
`SpeaksProtocol`, `Supports` and `AllowsInfrena` (`pkg/pluginmanifest/manifest.go:101-184`, `288`).
But no infrena command reads one yet. The reader it was written for, `infrena plugins install`, is
planned but not built (`PLAN.md` §31.1, Phase B; §31.2, "Where infrena reads it"). Loading a plugin
from the search path never looks at `plugin.yaml`. Ship it anyway. Your release gate checks
against it (section 13). And once install exists, it will read the manifest at each release tag, so a
release tagged without one stays without one. The plan is to install such a plugin with only a
warning (`PLAN.md` §31.2), but then none of the compatibility checks below apply to it.

### Its shape follows from its purpose

The manifest is fetched over the network and read **before any binary is downloaded**, so a search
can answer "is this compatible with the infrena I'm running, and is there a build for my machine?"
without downloading anything else. It will be read by infrena builds released for years afterwards.
Every design decision below follows from those two facts.

| Key | Required | Meaning |
| --- | --- | --- |
| `manifest` | yes | The format version of this file. Checked first, before any other key. |
| `name` | yes | The plugin's name: binary `infrena-plugin-<name>`, `Plugin.Name()`, and every type's prefix. |
| `version` | yes | `MAJOR.MINOR.PATCH`. Must equal the tag the file is read at. |
| `protocol` | yes | The protocol versions **this release's binary** speaks: exactly one for a plugin built with `pkg/pluginsdk`. See [below](#protocol-what-this-releases-binary-speaks). |
| `platforms` | yes | `GOOS/GOARCH` for every build you publish. |
| `description` | yes | One line, for a search result. |
| `infrena` | no | The infrena releases this plugin is known to work with, in `pkg/semver` syntax. |
| `source` | no | Where the plugin lives, for a search result to link to. |

(`PLAN.md` §31.2, the key table.)

Validate your own manifest with `pkg/pluginmanifest.Parse` (`infrena/pkg/pluginmanifest/manifest.go`'s
`Parse`) — the same parser `infrena plugins install` will use — rather than a hand check a typo
could pass.

### `protocol`: what this release's binary speaks

**infrena:** `PLAN.md` §31.2, amended 2026-09-14 (infrena `b5f361b`, included in v0.3.0), defines
`protocol` as the plugin protocol versions **this release's binary** can speak. For a plugin built
with `pkg/pluginsdk` that is exactly one, the `pluginproto.Version` of the infrena it was built
against, which the SDK puts in the handshake (`pkg/pluginsdk/serve.go:92-96`). A longer list is only
for a plugin that hand-rolls the protocol and genuinely negotiates several. Don't copy the host's
`Supported` set: `[2, 1]` claims a protocol your binary cannot speak.

Three consequences follow:

1. **It never goes stale.** This repository's v0.1.1 was released saying `protocol: [1]`, and that
   stays true: that binary announces 1 and always will.
2. **A host protocol bump forces no re-release.** The old version stays in `Supported`, so an existing
   release keeps loading and keeps describing itself correctly.
3. **Your next release changes `protocol` in the same commit as its infrena `require` bump**, because
   the rebuilt binary announces the new number. This repository went to `protocol: [2]` in the commit
   that moved `go.mod` to `github.com/infrata/infrata v0.3.0`, as the module was named then, and to
   `protocol: [3]` in the commit that required `github.com/infrena/infrena v0.6.0`.

Two checks enforce the third here. `internal/fake/manifest_test.go` requires `protocol` to be exactly
`[pluginproto.Version]`, and `scripts/release-check` refuses a manifest whose `protocol` is not
exactly the version the built binary's handshake announces ([section 13](#13-the-release-gate)).

### Why it is read at a release tag

The file on your default branch describes **unreleased** code. Reading it to judge `v1.2.0` answers
the wrong question once `main` has moved on to `v1.3.0`. That is also the mistake an implementer makes
by default, because the `HEAD` URL is the obvious one. The manifest holds two kinds of information:
identity (`name`, `description`, `source`), which is the same on every ref, and the compatibility of
one version (`version`, `protocol`, `platforms`, `infrena`), which is not. One file can serve both
only because it is always read at a tag (`PLAN.md` §31.2, "READ IT AT THE TAG").

### Why the format is versioned when infrena's configuration is not

infrena's configuration language deliberately has no version number. It rejects unknown keys, so an
older infrena that meets newer syntax stops and names the key it didn't understand (`PLAN.md` §61.2).
That works because configuration is written and read by the same person, at the same time, on one
machine.

A manifest is different. A plugin author writes it, and infrena builds read it for years afterwards
with no way to upgrade the reader in step. If every reader rejected unknown keys, a 2026 infrena could
never install a 2027 plugin. So readers reject unknown keys only for a `manifest` version they know,
and tolerate them with a warning for a version they don't (`PLAN.md` §31.2, "Why the format is
versioned").

### Why `infrena` is optional

A missing `infrena` key means **unconstrained**. Don't write `infrena: ">= 0.0.0"` to say "works with
anything". It is a real constraint that means something subtly different, and it adds noise. State a
floor only when you know of a release your plugin does not work with. When one is stated, a
development build of infrena is exempt, as it is from a project's own floor (`PLAN.md` §31.2,
"Compatible means three things"). Validate your `infrena:` string with `semver.ParseConstraint`
(`pkg/semver/semver.go:126`) so you use the same parser infrena will.

**infrena:** nothing enforces the field at runtime today. The check belongs to `infrena plugins
install` (`PLAN.md` §31.2, "Where infrena reads it"; designed in §31.3, not built), and the handshake
doesn't carry it. `pkg/pluginmanifest.AllowsInfrena` exists, but nothing in infrena calls it yet. A
host older than your floor still loads your plugin. Today the field is documentation, and whatever
your own release gate makes of it.

**infrena:** "development build" means a host reporting `0.0.0` (`AllowsInfrena`,
`pkg/pluginmanifest/manifest.go`). Now that infrena has tags, a plain `go build` of an infrena checkout
isn't one. Go stamps the version from git: a clean checkout at `v0.2.0` reports `0.2.0`, and one commit
past it reports `0.2.1-0.<time>-<hash>`, which compares as `0.2.1`. Measured 2026-09-13; a checkout at
`v0.3.0` likewise reported `infrata 0.3.0 (45deb30, …)` (2026-09-14, before the rename), and infrena
`main` just after the rename, before `v0.4.0` was tagged on that same commit, reported
`infrena 0.3.1-0.20260914150359-e2be8bf36135 (e2be8bf, …)`, which compares as `0.3.1`. Once tagged, the
same checkout reports `infrena 0.4.0 (e2be8bf, …)`.

**Recommendation:** make the floor the release your CI builds against, and check it there. The e2e
test `TestTheInfrenaUnderTestSpeaksTheManifestsProtocol` reads the host's version and checks the floor
one of two ways. A host reporting a release (no suffix), such as CI's host built from the tag `go.mod`
requires, must satisfy the floor outright, so a floor above the tested release fails CI. A host
reporting a suffix is a development build, and gets `AllowsInfrena`'s own rule rather than a second
copy of it: `0.0.0-dev` is exempt, and a pseudo-version compares as its `MAJOR.MINOR.PATCH`, so it
passes exactly when it was built after a release the floor admits (`0.4.1-0.<time>-<hash>` passes
`>= 0.4.0`; a checkout from before `v0.4.0` fails). This repository's `infrena: ">= 0.6.0"` is the
release `go.mod` requires, and the first that understands the `References` its schema declares.
`internal/fake/manifest_test.go` separately checks the floor refuses `0.5.x` and admits `0.6.0`.

### What it deliberately leaves out

- **Checksums.** They can't exist until the binaries are built, so a checked-in manifest can't hold
  them honestly. `SHA256SUMS` is a release asset instead (section 13). A future `plugins.lock` will
  record checksums per platform on the user's side (`PLAN.md` §31.1, Phase B).
- **Asset names and download URLs.** A naming convention replaces them: install constructs the name
  (section 13). One convention is better than a field every author can get wrong.
- **Resource types.** `name` already implies them. A plugin serves `<name>.*` and the host refuses
  anything else, so "which plugin provides `hetzner.server`?" can be answered from the name alone.

(`PLAN.md` §31.2, "What the manifest deliberately does not carry".)

---

## 13. The release gate

A release has three version numbers that must agree: the **git tag**, `plugin.yaml`'s **`version`**,
and the version the **built binary reports** in its handshake. Your release workflow must refuse to
publish unless they do (`PLAN.md` §31.2, "What a plugin repository owes its own manifest"). This
repository's gate also checks one more fact the manifest states about a release: that `protocol` is
exactly the protocol version the same handshake announces.

### Why a release step, not a test

You could write a unit test comparing `plugin.yaml` to a constant in the code. That is weaker in two
ways. Someone can delete or skip a test, but a failing release step blocks the release. And a test
checks source code, not the binary: it can't catch a build whose `-ldflags` stamp went wrong. The gate
here runs in `.github/workflows/release.yml`'s `release` job, after CI has passed and the job has
switched to the tagged infrena module, before anything is built or published (`release.yml:43-48`).

### Why `Version()` defaults to `0.0.0-dev`

Stamping at release time only proves something if an unstamped build reports a **different** value.
Suppose `Version` defaulted to `"0.2.0"`, the same as `plugin.yaml`. Then a release whose `-X` flag
named the wrong symbol would still report `0.2.0`, correct by coincidence, and the gate would pass.
Go's linker ignores an `-X` for a symbol that doesn't exist, without any error
(`scripts/release-check:7-8`). With the default at `0.0.0-dev`, a broken stamp shows up as a
mismatch. `scripts/scripts_test.go:135-146` points `-X` at a nonexistent variable and checks that the
gate refuses, reporting `0.0.0-dev`. It also gives bug reports honest versions: a build from a
checkout never claims to be a release (`internal/fake/plugin.go:15-20`).

### How `scripts/release-check` reads the version

It needs no infrena at all. It builds the binary with the same stamp a release uses, then runs it
with the cookie set and an empty stdin. The SDK writes the handshake and exits, and the script pulls
out the version field:

```bash
# scripts/release-check:38-45
CGO_ENABLED=0 go build -trimpath -ldflags "-X ${symbol}=${version}" -o "$work/infrena-plugin-fake" ./cmd/infrena-plugin-fake

# The binary refuses to start without the host's cookie. With it and an empty stdin, it writes
# its handshake {"protocol","name","version"} and exits. Captured whole, not piped to head,
# so pipefail cannot turn an early-closed pipe into a false failure.
out="$(INFRENA_PLUGIN_COOKIE=release-check "$work/infrena-plugin-fake" </dev/null 2>/dev/null)"
handshake="${out%%$'\n'*}"
reported="$(printf '%s' "$handshake" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
```

Before that, it checks the tag has the form `vMAJOR.MINOR.PATCH` (`release-check:15-20`) and that the
tag agrees with `plugin.yaml`'s `version`, tolerating CRLF line endings (`release-check:25-33`). After
the version, it reads the handshake's `protocol` field and refuses unless `plugin.yaml` says exactly
`[<that number>]` (`release-check:52-66`). A manifest left at the previous release's protocol after a
`require` bump, or listing the host's whole `Supported` set, can't be published. Each failure is
tested in `scripts/scripts_test.go`, the protocol one by
`TestReleaseCheckRefusesAManifestProtocolTheBinaryDoesNotSpeak`.

### Archive names and `SHA256SUMS`

`infrena plugins install` will build the download name from a convention rather than read it from
anywhere (`PLAN.md` §31.2):

```
infrena-plugin-<name>_<version>_<goos>_<goarch>.tar.gz      (.zip for windows)
```

`scripts/build-release` builds every platform `plugin.yaml` lists (`build-release:16`). It archives
each build under that name, with the binary, `plugin.yaml` and `README.md` inside a top-level
directory of the same stem (`build-release:29-46`). `scripts/scripts_test.go:150-189`
(`TestBuildReleaseNamesArchivesByTheInstallConvention`) checks the names and contents. A wrongly named
archive is a release nobody can install.

After the build, the workflow checksums every archive into a `SHA256SUMS` release asset
(`.github/workflows/release.yml:53-55`), and then publishes (`release.yml:57-60`). The checksums live
in the release, not the manifest, because they don't exist until the build does. The tests and every
build run against the infrena release `go.mod` requires, fetched as a module with
`INFRENA_CHECKOUT_TOKEN` ([section 11](#ci-needs-credentials-for-infrena)).

To release: bump `version` in `plugin.yaml`, commit, tag `v<that version>`, and push the tag. If the
tag, manifest or binary disagree, the workflow stops before publishing anything.

---

## 14. A real cloud: AWS as the worked example

Everything above applies to any cloud. This section is about what changes when the cloud is real: real
credentials, many regions, pagination, throttling, eventual consistency. It uses the next plugin,
`infrena-plugin-aws` (repository `infrena-provider-aws`, module
`github.com/infrena/infrena-provider-aws`), as the example.

Each topic separates two kinds of statement, and labels them:

- **infrena:** what infrena does, checked against its source, with the file and symbol.
- **Recommendation:** what an AWS plugin should do. Where it names the AWS SDK for Go v2, the API was
  checked against the SDK's developer guide (`docs.aws.amazon.com/sdk-for-go/v2/developer-guide`) or
  its package documentation on `pkg.go.dev`. The design is still a recommendation, not something
  infrena enforces.

Type names below, such as `aws.vpc`, `aws.subnet` and `aws.rds`, are illustrations drawn from
infrena's initial AWS resource list (`PLAN.md` §32). This section is not the design of the whole
plugin.

### The repository, and the SDK dependency

**infrena:** AWS is a plugin in its own repository, building `infrena-plugin-aws`, not a directory
inside infrena (`PLAN.md` §31.1, "Where the code lives"; §50.1). Its module path must be outside
`github.com/infrena/infrena/` ([section 11](#keep-your-module-path-outside-infrenas)).

The AWS SDK for Go v2 is a third-party dependency, and **that is fine in a plugin's own module**.
Keeping heavy dependencies out of infrena's core is part of why plugins are separate processes. The
protocol itself adds no third-party dependency (`PLAN.md` §31.1, opening paragraphs), and
`hashicorp/go-plugin` was rejected partly because gRPC would go "far past a dependency budget that so
far holds two libraries"
(`PLAN.md` §31.1, "Alternatives rejected"). Moving the AWS SDK out of the core module's dependency
budget was also the original reason for giving AWS its own module (§31.1, "Where the code lives").
Your plugin's `go.mod` can require whatever the cloud needs. infrena's never sees it.

**infrena:** every command starts its plugins, including `validate`, `explain` and `graph`. So a plugin
with expensive startup, "like AWS SDK credential resolution", pays for it on every run (`PLAN.md`
§31.1, "What this costs, recorded before it is built"). **Recommendation:** do no network work in
`Definitions`, and keep `New` to what configuring an instance needs.

### Credentials and accounts

**infrena:** a `providers:` entry holds two separate maps (`internal/config/declarations.go:43-54`,
`ProviderDecl`). Every key other than `plugin`, `name`, `default` and `defaults` is the plugin's own
configuration (`internal/config/providers.go:87-111`, the `default:` branch of the key switch). It
reaches `New` resolved, as `provider.Config.Values` (`pkg/provider/provider.go`, `Config`). The keys under `defaults:` are
**attribute defaults for resources**. The plugin is never sent them
([Regions](#regions-a-default-on-the-instance-overridden-per-resource) shows what they're for). infrena
has no credential mechanism of its own ([section 8](#8-credentials)), and the plugin process inherits
infrena's environment.

**Recommendation:**

- **Load credentials the way AWS tooling does.** Use `config.LoadDefaultConfig(ctx, …)` from
  `github.com/aws/aws-sdk-go-v2/config`. It reads the standard environment variables and the shared
  `~/.aws/config` and `~/.aws/credentials` files, and resolves the SDK's other credential sources. A
  user who can already run `aws sts get-caller-identity` shouldn't have to configure anything again.
- **Accept explicit overrides as named `config` keys**, for example `profile`, passed as
  `config.WithSharedConfigProfile(profile)`. For a role to assume, take `assume_role_arn` and wrap the
  loaded credentials with `stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), arn)` inside
  `aws.NewCredentialsCache(…)`.
- **One instance per account.** Two accounts means two `providers:` entries with different `name:`s,
  and resources choose one with `provider:` (`internal/config/decode.go`, the `provider` case).
  **Regions are not a reason for another instance** (next subsection).
- **Refuse unknown keys**, naming what you accept. A misspelled `profil:` that is silently ignored
  falls back to the default credential chain, which may be a different account.
- **Make `New`'s error actionable, but weigh the cost of checking eagerly.** Calling
  `cfg.Credentials.Retrieve(ctx)` in `New` makes a missing credential fail as a configuration error
  against the `providers:` entry instead of failing halfway through an apply — but that pulls against
  the earlier advice to keep `New` cheap. Every command that compiles constructs instances at stage
  4.5, including `validate` and `graph`, so an SSO or assume-role `Retrieve` would then hit the
  network on every `validate`, not only on `plan` and `apply`. If that cost matters to your users,
  defer the check to the first real API call instead and accept the later failure point. Whichever
  you choose, say which sources were in play and what to set, for example: `no AWS credentials found
  for instance "prod" (profile "prod"): run "aws sso login --profile prod", or set
  AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY`.
- **Never log credentials**, and never format them into an error ([section 8](#8-credentials)). An AWS
  debug log of signed request headers is exactly that.

### Regions: a default on the instance, overridden per resource

The requirement: one plugin instance works across many regions. The user sets a default region once
and overrides it on any resource, including from a variable.

**infrena:** there is no region infrena gives you for free.

- **The discover request carries no region.** `provider.DiscoverRequest` and
  `pluginproto.DiscoverParams` declare only `Types` (`pkg/provider/provider.go`,
  `pkg/pluginproto/proto.go`) — there is no `Region` field to read. A plugin that scans several
  regions takes them from its own instance `config:` (below); the host cannot supply them because it
  does not know what a region IS for your cloud. (v0.2.0 still declared a `Region` field the host
  never set; v0.3.0 removed it, so a plugin that read it no longer compiles.)
- **There is no ambient `region` (or `account`).** infrena seeds exactly two process variables into
  every scope: `environment` and `project`, written `${var.environment}` and `${var.project}` like
  any other variable (infrena `internal/variables/resolve.go`, `ProcessVariables`; `PLAN.md` §6.3,
  §10.5 and §12.1). `region` is an ordinary variable name — declare it under `variables:` like any
  other and write `${var.region}`, or use a differently-named one, as the AWS example below does
  with `${var.aws_region}`. (Before v0.3.0, `region` was reserved but never supplied.)

What does work is **instance `defaults:`**. In `internal/compiler/schema.go:49-51`, compilation runs
`applyInstanceDefaults`, then `applyDefaults` (the schema's own `Default`), then `checkRequired`.
That order gives three rules:

- A `Required` attribute can be satisfied by the instance's `defaults:`.
- A value the resource writes itself always wins. `applyInstanceDefaults` skips any attribute already
  present, any the type doesn't declare, any computed attribute, and any default of the wrong kind.
  It marks what it fills as a provider-instance default (`schema.go:309-326`).
- A required attribute set nowhere is refused at compile time, before any AWS call.

`defaults:` values are resolved like configuration, so they can use variables
(`internal/providers/resolve.go:75`). A `defaults:` key that no type of the plugin declares is refused
(`internal/providers/prepare.go:335`, `checkDefaults`); a key only some types declare is fine. The
ladder is documented in `PLAN.md` §12.1, "Resource-attribute defaults live under `defaults:`": the
resource's own value, then the instance's `defaults:`, then the schema default. A map is replaced
whole, not merged.

Here is that ladder, run against this repository's fake plugin with `fake.database.size`, which has
a schema default of `10`. The instance says `defaults: {size: ${var.db_size}}`, one database writes
nothing, and one writes `size: 50` (re-run against infrena v0.5.0):

```
$ infrena plan dev --var db_size=20
...
  + fake.database.uses_default
      endpoint: (known after apply)
      engine: "postgres"
      network: (known after apply)
      size: 20 [default, from provider instance default]

  + fake.database.writes_its_own
      endpoint: (known after apply)
      engine: "postgres"
      network: (known after apply)
      size: 50
```

A misspelled key fails closed:

```
Error: provider instance "fake" defaults "regoin", which no resource it serves accepts
```

**Recommendation — the region model:**

- **Every regional AWS type declares `region`** as `{Kind: value.KindString, Required: true, ForceNew:
  true}`. `ForceNew` is correct because an AWS resource can't move between regions: a changed region
  is a different resource. Global types, such as IAM roles and policy attachments or Route 53 records,
  don't declare it.
- **The instance supplies the default** through `defaults: {region: …}`, and **a resource overrides
  it** with its own `region:`, as a literal or a variable. The plugin reads the region from the
  resource's attributes. It never needs a region in its own configuration for CRUD.
- **Changing the instance's default region replaces every resource that relies on it.** `region` is
  `ForceNew`, and the ladder above fills it from `defaults:` for any resource that omits it. Editing
  `defaults: {region: …}` therefore plans a destroy-and-create of every regional resource that
  inherited the old default, not just the ones a user meant to move. Before changing it, set
  `region:` explicitly on any resource that must not move. For a resource that must never be
  replaced this way, infrena's `lifecycle: prevent_destroy: true` turns that plan into a refusal
  instead of a destroy-and-create (`PLAN.md` §15, §38; enforced at plan time in
  `internal/compiler/validate.go`).
- **Discovery's regions come from `config:`**, for example `discover_regions: [us-east-1, eu-west-1]`.
  A plugin never receives `defaults:`, and the discover request carries no region field, so its own
  configuration is the only place that list can come from. **Keep it resolvable without an
  environment**, because `discover` takes none (below): a literal list, a variable with a
  `default:`, or one set in `vars/default.yml` all work; a variable only an environment sets needs
  `--var` on `discover` itself, or the instance holding it is refused by name.
- **One loaded SDK configuration, a client per region.** Call `LoadDefaultConfig` once in `New`. Then
  either build a client per region with `ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.Region =
  region })`, or override the region on a single call with the same kind of functional option. The
  SDK guide documents per-operation overrides as safe for concurrent use.
- **Make the provider ID carry the region**: `<region>/<id>`, for example `us-east-1/vpc-0abc123`.
  `Import` receives only a type and an ID, with no region (next-but-one subsection). Say so in
  `ImportSpec.Description`.
- **Accounts stay separate instances.** `profile` or `assume_role_arn` stays in `config:`.

```yaml
variables:
  aws_profile: {type: string}
  aws_region: {type: string}
  dr_region: {type: string}

providers:
  - plugin: aws
    profile: ${var.aws_profile}
    discover_regions: [us-east-1, eu-west-1]
    defaults:
      region: ${var.aws_region}

resources:
  vpc:
    type: aws.vpc
    cidr: 10.0.0.0/16          # region from the instance default
  dr_vpc:
    type: aws.vpc
    cidr: 10.1.0.0/16
    region: ${var.dr_region}   # overridden per resource, from a variable
```

A reference can also reach inside a value: `${var.azs[0]}` is an entry of a list variable,
`${var.tags.team}` a key of a map variable, and `${vpc.tags.Name}` a key inside a resource's map
attribute (infrena v0.5.0, `PLAN.md` §10.5). An index is an integer literal only, and an
out-of-range index or a missing key is a compile-time error, not a value known after apply.

**This is infrena's current design: `plan`, `apply`, `refresh`, `destroy` and `import <env>` all
resolve `providers:` against the environment named on the command line; `discover` alone has none to
resolve it against.** `plan` and `apply` resolve it the ordinary way, through `compiler.Compile`.
`refresh` and `destroy` — which never compile — build the instance through `compiler.VariableScope`
(`internal/compiler/compile.go`), the same stages 1-4 `Compile` shares, handed the environment named
on the command line, so `defaults: {region: ${var.aws_region}}` above works on all four. `refresh` and
`destroy` therefore accept `--var` and `--var-file`, which they used to refuse outright. `plan`/`apply`
run against an environment removed from `environments:` but still holding state — an "orphaned"
environment, §6.1 — take that same state-only path with an EMPTY environment, because the
environment's per-environment values went away with its declaration.

`import <env>` takes that same state-only path as `refresh`/`destroy`, handed *its own* environment:
`discoveryRegistry` (`internal/cli/context.go`) takes the environment explicitly, and
`newImportCommand` passes the one named on its own command line rather than passing none
(`internal/cli/import.go`, corrected 2026-09-13 — it used to pass `""` here, the bug this repository
caught: an `import dev` holding "dev" on its command line still could not resolve a per-environment
`providers:` value, failing exactly like a bare `discover`). `discover` is the only caller that
legitimately has no environment, and passes `""` for it.

`discover` therefore resolves only what doesn't need an environment — a declared `default:`,
`variables.yml`, `vars/default.yml`, `--var` — and refuses an instance still holding an unresolved key
BY NAME rather than guessing at it (`internal/cli/context.go`,
`registerStateInstances`/`refuseUnresolvedInstances`).

infrena's `PLAN.md` §12.1 (amended 2026-09-13 twice) records why any of this is possible: resolving
variables is stages 1-4 and needs no registry, no plugins, no resources and no modules, which is what
makes it available to a command that never compiles — and why `import <env>`'s own environment was
there to pass down all along.

Checked against a built infrena with this repository's plugin, using `providers: [{plugin: fake,
cloud: ${var.cloud_file}}]` with `cloud_file` set ONLY in `vars/dev.yml` (no `default:`, which every
command resolves without an environment): `apply dev` created a resource, and — after hand-adding an
untracked one to the cloud file — `import dev fake.network.net-77 --generate` resolved `cloud_file`
from `vars/dev.yml` and imported it, with no `--var` needed. Bare `discover` — no environment to
resolve the variable against at all — refused by name:

```
Error: provider "fake"'s configuration key "cloud" could not be resolved
  at infra.yml:11:5

  No environment was resolved, so a value that differs per environment cannot be determined —
  either this command takes no environment, or the one named is no longer declared in
  configuration. Running anyway would use whatever the plugin defaults to, which may be a
  different account than the one you mean.

  Suggested action:
    Pass the value with --var, or use a literal here.

Error: provider instances could not be configured
```

(That detail text also covers the orphaned-environment case above, which is why it no longer says
"this command does not take one" — that was true only of `discover` and was briefly false of
`import` too.) `discover --var cloud_file=...` resolves and runs. So give `profile`,
`discover_regions` and `defaults: {region: …}` a way to resolve without an environment (a literal, a
`default:`, or `vars/default.yml`) if you also run bare `discover`, and otherwise be ready to pass
`--var` to `discover` itself — `import <env>` no longer needs it for a value only an environment
sets. A resource's own region, like `dr_vpc`'s above, reaches state as a plain value, so `refresh`
and `destroy` never needed the variable for it — and neither does removing an environment and
applying, which is how a user tears one down.

**Import selectors don't carry a provider instance either**, which matters the moment two accounts
exist — see [Import IDs](#import-ids) below for the ambiguity refusal and `--provider`.

### Discover against a real API

**infrena:** `Walk` asks each **instance** only about types it offers and that were requested. It
skips an instance that offers none of them (`internal/discovery/walk.go:48-59`). It then sorts every
result by type and provider ID before naming them (`walk.go:79-85`), so your order doesn't affect
output. For how the host treats undeclared types and attributes, see
[`Discover` and `Import`](#discover-and-import): an undeclared type is skipped, but an undeclared
attribute on a declared type fails the whole discovery.

**Recommendation:**

- **One API family per type, only for the requested types.** `aws.vpc` means `DescribeVpcs`, and
  `aws.rds` means `DescribeDBInstances`. Don't list what wasn't asked for.
- **Paginate with the SDK's paginators**, for example `ec2.NewDescribeVpcsPaginator(client, params)`,
  looping on `HasMorePages()` and `NextPage(ctx)`. **Check `ctx` between pages**: abandoning a read
  loses nothing ([section 6](#6-cancellation)).
- **Loop over `discover_regions`** and use each region's client.
- **Include resources infrena did not create.** Finding those is the only reason discovery exists.
- **Return only attributes your schema declares**, including `region`.
- Sort by provider ID if you like, for stable unit tests. The host sorts anyway.

### Import IDs

**infrena:** `infrena import <env> <type>.<provider id>` doesn't pass an arbitrary string to your
plugin. It runs discovery, looks the selector up among the results as `<type>.<provider id>`, and
calls `Import` with the discovered type and provider ID (`internal/cli/import.go:161`,
`selectForImport` at `:232`). A selector discovery didn't return is refused with `not found by
discovery`. A slash in the ID is fine: the selector is matched whole, so
`aws.vpc.us-east-1/vpc-0abc123` works if `Discover` returned `us-east-1/vpc-0abc123`. **A resource in
a region your instance doesn't scan can't be imported at all.** `selectForImport` only matches what
`Discover` returned, and `Discover` only visits `discover_regions`. A VPC sitting in a region missing
from that list never becomes a selector to import — there is no separate error naming the region, it
simply isn't offered.

**A selector names no provider instance** — `<type>.<provider id>` is the whole syntax — and a
provider ID is unique within an account, not across them (§12.1), so two instances of your plugin can
each hold `net-1`. `narrowToSelectors` (`internal/cli/import.go:260`, corrected 2026-09-13) refuses an
ambiguous selector rather than silently picking one, naming every instance that holds it:

```
$ infrena import dev fake.network.net-1
Error: fake.network.net-1 exists in more than one provider instance: acct1, acct2
A selector names no instance, and a provider ID is unique within an account rather than across
them, so this would adopt one of them arbitrarily.
Narrow it with --provider <instance>
```

`import <env> --provider <instance>` narrows the whole command to that instance, so it also answers
"adopt everything discovery found in this one account" when no selectors are given at all — the
no-selector form otherwise has no way to say that. A `--provider` naming an instance discovery found
nothing for is refused by name rather than treated as an empty, successful import (a typo would
otherwise silently import zero resources). Verified against a built infrena: two fake instances each
holding an untracked `net-1`, `import dev fake.network.net-1` produced the error above, and `import
dev fake.network.net-1 --provider acct2` then imported it.

**Recommendation:**

- **Keep provider IDs unique where you can, and prefer the `<region>/<id>` form everywhere IDs
  appear** — in `Discover`, `Import`, and the provider ID you return from `Create` — with AWS's own
  IDs: `vpc-…`, `subnet-…`, `sg-…`, or an RDS instance identifier. That resolves a collision within
  one account. A cross-account collision no longer picks a silent winner — it is refused, naming both
  instances, and the fix is `--provider <instance>` — but a project importing from more than one AWS
  account should still expect the collision and reach for `--provider` rather than a bare selector.
- **Check the type against the ID and refuse a mismatch**, as the fake does
  ([`Discover` and `Import`](#discover-and-import)). `aws.subnet` with `us-east-1/vpc-0abc123` names a
  VPC. Refuse it, naming both, before any API call. Where the prefix doesn't settle it, the describe
  call does. A malformed ID gets its own error code from EC2 (`InvalidVpcID.Malformed`, for example).
  Report it as the user's mistake.
- `(nil, nil)` from `Import` becomes `no <type> with id "<id>"` (`adapter.go:245-247`). Return that
  for a well-formed ID that doesn't exist.

### Errors and retries

**infrena:** [section 5](#5-errors-and-retries) is the authority. The table there applies unchanged:
creates and deletes are retried only on `SafeToRetry`; updates also on `ConditionallyRetryable`;
reads, discovery and import never. "Retried" means up to three attempts in total. A value outside the
three classes is never retried.

**Recommendation — map SDK errors like this:**

| What happened | How to recognise it (SDK v2) | Classify as |
| --- | --- | --- |
| Throttled: AWS refused the request before acting on it | `errors.As(err, &apiErr)` with `apiErr smithy.APIError`, and `apiErr.ErrorCode()` is a key of `retry.DefaultThrottleErrorCodes` (for example `Throttling`, `RequestLimitExceeded`, `TooManyRequestsException`) | `SafeToRetry` |
| A server fault, a timeout, or a connection lost after the request may have been sent | `errors.As(err, &re)` with `re *smithyhttp.ResponseError` (or `*awshttp.ResponseError`, which embeds it), and `re.HTTPStatusCode() >= 500`; `errors.Is(err, context.DeadlineExceeded)`; a `net.Error` in the chain | `ConditionallyRetryable` |
| Validation, access denied (`UnauthorizedOperation`, `AccessDenied…`), not found on a mutation, a malformed ID | any other `smithy.APIError` | `NotSafeToRetry` |
| Anything you don't recognise | — | `NotSafeToRetry` |

**Check the rows in order, throttle first.** EC2's API reference ("Error codes") lists
`RequestLimitExceeded` among its *server* error codes, which carry a 500-series status, so a status
check that ran first would classify a throttle `ConditionallyRetryable`.

**Why the status code, not `apiErr.ErrorFault()`.** The EC2 SDK package declares no modelled error
types: every operation's error deserializer (for example `awsEc2query_deserializeOpErrorCreateVpc`)
returns `&smithy.GenericAPIError{Code, Message}` with `Fault` unset, so `ErrorFault()` is
`FaultUnknown` for every EC2 error. Services with modelled exceptions do set it on those: IAM's
`types.ServiceFailureException.ErrorFault()` returns `smithy.FaultServer`. But a code that isn't
modelled falls through to the same `GenericAPIError` in IAM, Route 53 and RDS too. `ErrorFault` is
unreliable across services. The status is not: `awshttp.ResponseErrorWrapper` wraps every error that
came with an HTTP response. (Checked in `service/ec2` v1.332.0, `service/iam` v1.64.0,
`service/route53` v1.70.0, `service/rds` v1.129.0, `aws-sdk-go-v2` v1.47.0, `smithy-go` v1.28.1.)

Mapping a server fault to `ConditionallyRetryable` rather than `SafeToRetry` is the safe default,
not a hedge: `ClassifyError` sees only the `error` value, and has no way to know whether the request
that failed carried a client token, which is what would make retrying it safe. AWS's own EC2
guidance, "Ensuring idempotency in Amazon EC2 API requests", recommends retrying a 500 specifically
for a request that included a client token — a narrower claim than "retry every 500", and exactly
why classifying blind should lean cautious.

Two SDK details make that table work. First, when the SDK's own retryer gives up, it wraps the last
error in `retry.MaxAttemptsError`, which has `Unwrap`, so `errors.As` still finds the `smithy.APIError`
inside. Second, keep `ClassifyError` a pure function of the error (section 5): infrena's plugin SDK
asks any one of your configured instances to classify, not necessarily the one that failed.

**Two retry loops.** The SDK retries by default. `retry.NewStandard` makes three attempts, and its
default retryables include throttling codes, HTTP 500/502/503/504 and connection errors. The package
documentation doesn't distinguish idempotent operations from others. infrena's executor then retries
what you classify `SafeToRetry` up to three times, so one throttled create can become nine requests,
with two backoff schedules multiplied. Choose deliberately:

- **For reads and discovery, let the SDK retry.** infrena never retries them, so the SDK's retryer is
  the only one there.
- **For a create with no idempotency token, don't let the SDK resend it silently.** If a
  `CreateVpc` connection drops after AWS acted, the SDK's retry makes a second VPC that nothing
  records. `CreateVpc` isn't in EC2's list of calls that accept a `ClientToken`. Disable SDK retries
  for that call, with `func(o *ec2.Options) { o.RetryMaxAttempts = 1 }` or a client built with
  `aws.NopRetryer`. A per-call `RetryMaxAttempts` equal to the client's own is skipped
  (`finalizeOperationRetryMaxAttempts` wraps the retryer only when the value differs), which is
  harmless, since the client already makes that many attempts. Then classify the failure honestly so infrena decides.
- **Where the API accepts a client token** (EC2 lists `RunInstances`, `CreateNatGateway` and
  `CreateRouteTable`, among others, in "Ensuring idempotency in Amazon EC2 API requests"), set one.
  A retry with the same token doesn't act twice.
- If you'd rather keep one backoff loop, set the SDK's attempts to 1 across the board, with
  `config.WithRetryMaxAttempts(1)`, and retry reads inside your plugin yourself.

**Error messages.** Include the operation, the region, the ID, AWS's error code and message, and the
request ID (`errors.As(err, &re)` with `re *awshttp.ResponseError`, then `re.ServiceRequestID()`).
Leave out anything from the request that could be a secret.

### Eventual consistency

**infrena:**

- **`Read` returning `(nil, nil)` means gone.** The adapter turns it into a nil state
  (`adapter.go:161-163`), `readOne` records that as an observation of absence
  (`internal/refresh/refresh.go:189-197`), and `operationFor` plans a resource that is in
  configuration and in state but observed absent as a **create**, noting "the provider no longer
  reports this resource; it will be recreated" (`internal/planner/planner.go:269-285`).
- **A read error is not absence.** It stops planning that resource with "A failed read is not
  evidence that anything was deleted" (`planner.go:207-217`, in `operationFor`).
- **`apply` doesn't read a resource back after creating it.** The state recorded is exactly what
  `Create` returned. So a VPC that EC2 hasn't propagated yet is first read on the next `plan` or
  `refresh`, which in CI can be seconds later.

The EC2 API is documented as eventually consistent: "the result may not be immediately visible to
subsequent API commands" (EC2 API reference, "Error codes", "Eventual consistency").

**Recommendation:**

- **Build `Create`'s result from the create response** (`CreateVpcOutput.Vpc`), never from a describe
  call made straight afterwards. Never return `(nil, nil)` ([section 2](#why-nil-nil-from-create-is-an-error)).
  If the response lacks something computed that the resource needs, poll for it, bounded, before
  returning. Use an SDK waiter such as `ec2.NewVpcAvailableWaiter(client).Wait(ctx, params, maxWait)`,
  or your own loop. If it times out, return an error that includes the ID you already have.
- **Don't report `NotFound` as gone too soon.** A `Read` that gets `InvalidVpcID.NotFound` (or the
  equivalent for another type) for a resource created moments ago may be looking before AWS
  propagated it. Returning `(nil, nil)` then makes the next plan propose a **second** VPC. Retry
  briefly with backoff before concluding it is gone, or return an error. An error is safe: it stops the
  plan rather than proposing a create. Keep the retry bounded, so a really deleted resource is still
  reported as gone.
- **Delete: treat `NotFound` as success** ([section 2](#2-the-two-interfaces-and-what-the-host-does-with-each-result)).
  Where a dependency's deletion is still propagating (a subnet whose ENIs are still detaching),
  wait inside the plugin.

### Modelling AWS resources

The flag vocabulary is [section 3](#3-modelling-a-resource-type)'s. A sketch, with the reasoning per
attribute:

```go
&schema.ResourceDefinition{
	Type:        "aws.vpc",
	Description: "An Amazon VPC.",
	Attributes: map[string]schema.Attribute{
		"region": {Kind: value.KindString, Required: true, ForceNew: true, Description: "AWS region"},
		"cidr":   {Kind: value.KindString, Required: true, ForceNew: true, Description: "Primary IPv4 CIDR block"},
		"tags":   {Kind: value.KindMap, Description: "Tags"},
		"id":     {Kind: value.KindString, Computed: true, Description: "VPC ID, e.g. vpc-0abc123"},
	},
	Capabilities: schema.Capabilities{Create: true, Read: true, Update: true, Delete: true, Import: true},
	ImportID:     schema.ImportSpec{Description: "<region>/<vpc id>, e.g. us-east-1/vpc-0abc123"},
}
```

- **`cidr` is `ForceNew`.** AWS lets you associate *additional* CIDR blocks, but not change or
  disassociate the primary block the VPC was created with (Amazon VPC User Guide, "VPC CIDR blocks").
  A changed `cidr` means a new VPC, and the plan must say *replace*.
- **`tags` updates in place.** Tags change without replacing anything, so no `ForceNew`. Remember the
  [`Update` contract](#the-update-contract): delete the tags `desired` no longer has, don't only
  add.
- **IDs are `Computed`.** AWS assigns them, and so are ARNs where the API returns one
  (`types.Subnet` has `SubnetArn`; `types.Vpc` has no ARN field, so the sketch declares none). A user who sets one gets "is computed and cannot
  be set".
- **An RDS master password is `Sensitive`**, and it needs one more thing: AWS never returns it.
  `rds/types.DBInstance` has `MasterUsername` and `MasterUserSecret`, but no password field. The
  planner treats an attribute configuration sets and the observed state lacks as a change, "not set on
  the resource" (`internal/planner/diff.go:108-114`, `diffAttributes`). So a `Read` that drops the
  password makes every plan propose an update, forever. **In `Read`, carry a write-only attribute
  forward from `current`**, since the API can't tell you it changed. That is a legitimate use of
  `current`. (Carrying *bookkeeping* forward is the host's job, [section 9](#9-what-the-host-enforces-so-you-dont).)
  Or offer RDS's managed secret (`MasterUserSecret`) and keep the password out of state entirely.
- **Recommendation: declare everything AWS always returns, and make what AWS chooses when the user
  doesn't `Optional` + `Computed`.** This is the converse of the password case
  ([section 3](#an-attribute-read-reports-but-configuration-omits)): an attribute that `Read` fills
  from every API response, but that configuration may omit and that isn't `Computed`, plans a change
  on every run. An optional `availability_zone` on a subnet is the worst case: EC2 always returns it,
  and it is `ForceNew`, so every plan would propose a replacement. Declare it
  `{Kind: value.KindString, Optional: true, Computed: true, ForceNew: true}`. A user may name a zone,
  AWS picks one otherwise, and an unset zone is never diffed, so it never forces a replacement
  ([section 3](#optional-with-computed-the-cloud-picks-unless-configuration-says) reproduces exactly
  this). Before infrena v0.3.0 the only honest choices were `Required` or `Computed`, and each took a
  choice away from the user. Keep plain `Computed` for what nobody may set, such as IDs and ARNs.
  Likewise, when `DescribeVpcs` returns no tags, leave
  `tags` out of the state `Read` returns. Returning it as `{}` makes every untagged VPC plan
  `tags: {} -> (absent)`.
- **Recommendation: declare `References` on every attribute that holds another resource's ID or
  ARN, naming which one.** `aws.subnet`'s `vpc_id` declares `&schema.Reference{Type: "aws.vpc",
  Attribute: "id"}`. A user then writes `vpc_id: ${vpc}` and cannot hand it an ARN by mistake, which
  is the complaint `PLAN.md` §14.3 was written from. An attribute that wants an ARN declares
  `Attribute: "arn"`, on a target type that has an `arn` attribute (the sketch's `aws.vpc` has none,
  so a reference to `aws.vpc.arn` would refuse to load). `tags` stays open, with no `Fields`: AWS tags
  take any key. §14.3 defines a reference only for a single-valued, top-level attribute. It says nothing
  about a list of IDs, such as an RDS subnet group's `subnet_ids`, and a `References` nested in
  `Fields` is refused, so model a list of IDs with explicit `${subnet_a.id}` references until infrena
  says otherwise.

#### Requirements are where a real cloud leans hardest

**infrena:** a `schema.Requirement` has a `Name`, the `Types` that satisfy it, `Optional` and a
`Description` (`pkg/schema/definition.go:13-18`). `checkRequirements` checks every non-optional
requirement before any provider is called. It reports `"<address>" is missing required <name>`, the
description, `Satisfied by a resource of type: …`, and suggests adding one
(`internal/compiler/validate.go:151-197`, diagnostic at `:188`). Know what it checks, which matters
more on AWS than on the fake: **a requirement checks that something of the right type exists in the
same account, and nothing more.**

- **Satisfied per provider instance, not across the project** — corrected 2026-09-13
  (`validate.go:113-151`, the comment on `checkRequirements`; keyed by instance+type at `:158`).
  Two instances are two accounts (§12.1): a subnet in one account's `us-east-1` no longer satisfies a
  VPC requirement by way of a VPC that exists only in a different account. It isn't a check that
  *this* subnet references *that* VPC, either — `Requirement` names no attribute to trace. The order
  resources are created in still comes from references like `vpc: ${vpc.id}`
  ([section 4](#4-requirements)).
- **Not region-aware, within an instance.** A requirement names types, not attributes, so a subnet
  requiring a VPC is satisfied by a VPC in any region of the *same* account — a subnet in
  `eu-west-1` is still satisfied by a VPC in `us-east-1` of that account. This is an intended limit,
  not a pending one: the real guarantee for region correctness is the reference (`vpc: ${vpc.id}`),
  which stages 6 and 7 validate.
- **State doesn't count, and this is intended too.** Only resources in the resolved configuration
  satisfy a requirement. A resource that exists in AWS but isn't declared, such as a VPC another team
  owns, doesn't satisfy one. `validate` contacts nothing, by design; moving this check to a stage
  that has state would make `validate` either weaker or state-dependent.

Both remaining limits would be answered by the same change — letting a `Requirement` name the
attribute it is satisfied by — and infrena is deliberately not guessing at that vocabulary before a
real cloud plugin says what it needs.

**Recommendation: `Requirements` is a pre-flight hint, not the correctness mechanism.** Declare what
AWS itself would reject a create without, but also model the dependency as a `Required` reference
attribute (`vpc_id: ${vpc.id}`) on every resource that needs one — that reference is what infrena
actually enforces end to end. A plugin that treats `Requirements` alone as the guarantee gets an
under-specified reference past `validate`: a subnet whose requirement is satisfied by *some* VPC in
its account can still carry a `vpc_id` pointing at the wrong one, or a literal ID instead of a
reference, and nothing here catches it.

```go
// aws.subnet
Requirements: []schema.Requirement{{
	Name: "vpc", Types: []string{"aws.vpc"},
	Description: "A subnet must be created inside a VPC",
}},

// aws.rds
Requirements: []schema.Requirement{
	{Name: "subnets", Types: []string{"aws.subnet"}, Description: "An RDS instance needs subnets for its DB subnet group"},
	{Name: "security_group", Types: []string{"aws.security_group"}, Description: "An RDS instance needs a security group"},
},

// aws.ecs.service
Requirements: []schema.Requirement{
	{Name: "cluster", Types: []string{"aws.ecs.cluster"}, Description: "An ECS service runs in a cluster"},
	{Name: "task_definition", Types: []string{"aws.ecs.task_definition"}, Description: "An ECS service runs a task definition"},
},
```

A project that declares an `aws.subnet` and no VPC in the same provider instance then fails before
anything touches AWS, in the same shape as the fake's example in section 4:

```
Error: "private_a" is missing required vpc
  at infra.yml:12:3

  A subnet must be created inside a VPC
  Satisfied by a resource of type: aws.vpc
  It must belong to the same provider instance, "main": another instance is another account, and
  resources in one cannot reach the other.

  Suggested action:
    Add a resource of type aws.vpc to provider instance "main".
```

(Illustrative, not a captured run: there is no AWS plugin to run yet. The wording comes from
`validate.go:180-193`, the same code that produced the fake's real output in section 4. The address,
instance name, position and description would come from the project and the schema.)

Use `Types` with more than one entry where AWS really does accept alternatives. Where users commonly
point at infrastructure they don't manage with infrena, such as an existing VPC passed in as a plain
ID, `Optional: true` is the honest choice. It records the requirement for `explain` without refusing
a valid project, at the cost of the early error.

### Testing without an AWS account

[Section 7](#7-testing-without-a-cloud-account)'s three layers apply unchanged. What AWS adds:

- **Layer 1 against a fake of the SDK client, not of your code.** Define a narrow interface holding
  only the operations you call. The SDK guide's unit-testing page uses exactly this pattern: a method
  with the client's signature, `(ctx, *Input, ...func(*Options)) (*Output, error)`. Implement it twice:
  the real `*ec2.Client` satisfies it, and a test double holds a map of VPCs. The SDK also publishes
  per-operation client interfaces, such as `ec2.DescribeVpcsAPIClient`, which the paginators accept.
  Test drift, a `NotFound` on read, a throttle, and read-after-create against the double.
- **Or an `httptest.Server`**, with the client pointed at it through `BaseEndpoint`:
  `ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.BaseEndpoint = aws.String(srv.URL) })`. This
  tests the real SDK's serialisation, error decoding and retryer. That is the right place to prove
  that a throttle response maps to `SafeToRetry` and that you set `RetryMaxAttempts` where you meant
  to. Use static test credentials, never the environment's.
- **Layer 2 through `pkg/plugintest`**, exactly as the fake does. It proves the host accepts your
  schemas (the `aws.` prefix, `region`'s kind, the reserved names) and that classification survives
  the pipe.
- **Layer 3: one binary-level suite behind a build tag** (`//go:build e2e`), running a real `infrena`
  against your built binary, pointed at the fake endpoint or double.
- **An optional live suite against a real account**, behind **its own** build tag (for example
  `//go:build live`) and its own credentials. Never run it in the default `go test ./...`. Give it
  a dedicated, empty account, tag everything it creates, and destroy in `t.Cleanup`. It is the only
  suite that can catch eventual-consistency and IAM surprises, and it should never be what a
  contributor without credentials runs by accident.
