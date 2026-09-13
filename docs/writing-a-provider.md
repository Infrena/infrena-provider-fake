# Writing an infrata provider

This guide is for a Go developer who knows their cloud's API and has never used infrata. By the end
you should be able to build `infrata-plugin-<yourcloud>`, test it without a cloud account, and
release it.

[`AGENT.md`](../AGENT.md) is the short reference: what to do. This guide explains why, because the
rules only make sense once you know what the host does with your answers. Every claim about infrata
below was checked against infrata's source. Every excerpt is copied from this repository (a working
plugin, `infrata-plugin-fake`) or from infrata's own tree, with the `path:line` it came from. Paths
starting `pkg/`, `internal/` or `PLAN.md` are in the infrata repository. All other paths are in this
one.

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
11. [Depending on infrata today](#11-depending-on-infrata-today)
12. [The manifest, `plugin.yaml`](#12-the-manifest-pluginyaml)
13. [The release gate](#13-the-release-gate)

---

## 1. What a plugin is

A plugin is an ordinary Go executable. infrata starts it as a child process, and the two talk over
the child's stdin and stdout in newline-delimited JSON: one JSON object per line. You never write
that transport yourself. A plugin's whole `main` is one call:

```go
// cmd/infrata-plugin-fake/main.go:9
func main() { pluginsdk.Main(fake.NewPlugin()) }
```

`pluginsdk.Main` reads requests, runs each one in its own goroutine, calls your methods, and writes
the responses (`pkg/pluginsdk/serve.go:109-147`). Because every request gets a goroutine, one
process serves every operation infrata runs in parallel. One process also serves every configured
instance of the plugin: two accounts of your cloud means one process holding two configured
clients, told apart by a handle (`pkg/pluginproto/proto.go:116-122`).

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

Everything your plugin writes to stderr is kept by the host. Under `--verbose`, infrata prints each
line prefixed with the plugin's name, as `[fake] …` (`internal/pluginhost/connect.go:137-147`,
`internal/cli/context.go:107-112`). Without `--verbose` the lines are not shown, but the last 20 are
always kept (`connect.go:86`). If the plugin exits unexpectedly, the error message quotes them under
"Its last output was:" (`internal/pluginhost/errors.go:105-117`).

That tail is often the only explanation a user gets. A plugin that dies of a missing credential
otherwise shows up only as "the plugin stopped responding" — infrata replaces the bare `io.EOF` a
closed pipe leaves behind with that sentence (`internal/pluginhost/errors.go:100-117`), because "EOF"
is a Go sentinel, not something a user should have to know means "the plugin exited". It also means
anything you print can end up in an error message, even without `--verbose` (see
[Credentials](#8-credentials)).

### The cookie

The host launches a plugin with `INFRATA_PLUGIN_COOKIE` set to a fresh random value
(`internal/pluginhost/connect.go:62-68`). Without it, `pluginsdk.Main` prints what the binary is and
exits 2 (`pkg/pluginsdk/serve.go:34-40`):

```
$ ./infrata-plugin-fake
fake is an infrata provider plugin: it is run by infrata, not directly.
Put it where infrata looks for plugins and name it in your `providers:` block.
$ echo $?
2
```

The check exists because a protocol program started from a terminal would otherwise sit silently
waiting on stdin, which looks exactly like a hang. The cookie is not a security boundary. The host
only checks that it is present (`internal/pluginhost/cookie.go:9-12`). With the cookie set and an
empty stdin, the plugin writes its handshake line and exits 0:

```
$ INFRATA_PLUGIN_COOKIE=x ./infrata-plugin-fake </dev/null
{"protocol":1,"name":"fake","version":"0.0.0-dev"}
```

`scripts/release-check` uses exactly that to read a built binary's version (section 13).

### Where infrata finds the binary

The binary must be named `infrata-plugin-<name>`, plus `.exe` on Windows
(`internal/pluginhost/connect.go:150-155`). infrata searches these places in order and uses the
first match (`connect.go:159-202`, `internal/pluginhost/loader.go:185-195`):

1. `--plugin-dir`, then `INFRATA_PLUGIN_PATH`
2. `<project>/.infra/plugins/`
3. `~/.local/share/infrata/plugins/`
4. `$PATH`

While developing, `--plugin-dir ./bin` is the shortest loop. With `--verbose`, infrata prints which
path each plugin was loaded from (`loader.go:112-114`).

---

## 2. The two interfaces, and what the host does with each result

You implement two interfaces from `pkg/provider/provider.go`. `Plugin` is the plugin before any
configuration. `Provider` is one configured instance of it.

The split exists to break a cycle. To configure an instance you need its resolved configuration.
Resolving configuration needs variables, variables need a compile, and a compile needs the resource
schemas. Schemas need no configuration: a server type is described the same way whichever account
it would be created in. So infrata asks for schemas first, and configures instances later
(`pkg/provider/provider.go:76-87`).

In the host, both interfaces are wrapped by an adapter, `internal/pluginhost/adapter.go`. It sends
your methods only what they need, and then **rebuilds** your answer under its own rules before
anything else in infrata sees it. The adapter has no bypass (`adapter.go:119-123`). Each row below
says what the host does to the result.

| Method | What your plugin is sent | What the host does with the result |
| --- | --- | --- |
| `Plugin.Name()` | nothing | Must match the `plugin:` name that selected the binary. A mismatch is refused with a message naming both (`client.go:98-106`, `errors.go:55-69`). Every resource type must also be prefixed `<name>.` (`adapter.go:79-85`). |
| `Plugin.Definitions()` | nothing | Validated when the plugin loads (see [section 9](#9-what-the-host-enforces-so-you-dont)). Any failure refuses the whole plugin. |
| `Plugin.New(cfg)` | instance name, that instance's resolved configuration, project directory (`pkg/pluginproto/proto.go:110-114`) | An error is reported as "provider instance … could not be configured", pointing at the `providers:` entry (`internal/providers/prepare.go:302-309`). |
| `Version()` (optional) | nothing | Sent in the handshake. A plugin that doesn't implement it reports `0.0.0` (`serve.go:343-348`). |
| `Provider.Read` | type, address, provider ID, attributes | `(nil, nil)` means the resource is gone (`adapter.go:161-163`). Otherwise rebuilt, keeping the bookkeeping from the state infrata already held (`adapter.go:164`). |
| `Provider.Create` | type, address, desired attributes | `(nil, nil)` becomes an error saying the resource may exist untracked (`adapter.go:173-175`). Otherwise rebuilt, with the address taken from the desired resource (`adapter.go:185-187`). |
| `Provider.Update` | current and desired, each as type, address, provider ID, attributes | `(nil, nil)` becomes the same error (`adapter.go:200-202`). Otherwise rebuilt from current. |
| `Provider.Delete` | type, address, provider ID, attributes | Error only; the host rebuilds nothing. Make deleting something already gone succeed, as the fake does (`internal/fake/provider.go:222-236`). Otherwise a resource someone removed by hand turns the next destroy into an error about a resource that no longer exists. |
| `Provider.Discover` | the types wanted, and a region | Any type your plugin doesn't declare is skipped. Declared types have their attributes checked (`adapter.go:219-233`). |
| `Provider.Import` | the type, and the cloud's own ID | `(nil, nil)` becomes `no <type> with id "<id>"` (`adapter.go:245-247`). The address is assigned by infrata's `import` command, never by you (`internal/cli/import.go:130-134`). |
| `Provider.ClassifyError` | *called in your process* | See [section 5](#5-errors-and-retries). |

### What is never sent

A resource in infrata's state carries bookkeeping your cloud knows nothing about: its dependencies,
its lifecycle flags (`prevent_destroy`, `retain`), and its creation and update timestamps. **None of
it is ever sent to a plugin.** The wire type has no fields for it (`pkg/pluginproto/proto.go:133-154`).
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
in a tag, a remote name or an error message (`proto.go:143-151`). But the address on a *result* is
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
		"%s reported no result from %s of %s, so infrata cannot record what now exists.\n"+
			"If the operation did take effect, that resource exists and is not in state: "+
			"check %s directly before re-running.\n"+
			"This is a defect in the plugin — a successful %s must report the resource it "+
			"acted on.",
		r.plugin.Name(), method, resourceType, r.plugin.Name(), method)
```

If your cloud's create call succeeds but you can't read back what it made, return an error that says
so and includes whatever ID you have.

### `Discover` and `Import`

`Discover` answers "what exists?", and that includes resources infrata did not create, which is the
only reason discovery exists. The fake reports every resource in its cloud file, including ones
without an infrata address (`internal/fake/provider.go:238-271`). Filter by `req.Types` in your
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
// internal/fake/definitions.go:25-47
		{
			Type:        "fake.database",
			Description: "A fake database. Requires a network.",
			Attributes: map[string]schema.Attribute{
				"engine": {Kind: value.KindString, Required: true, ForceNew: true, Description: "Database engine"},
				// A default is a datum, not a function: a schema has to survive a pipe.
				"size":     {Kind: value.KindInt, Description: "Storage in GB", Default: int64(10)},
				"password": {Kind: value.KindString, Sensitive: true, Description: "Administrator password"},
				"network":  {Kind: value.KindString, Description: "Network this database sits in"},
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

`infrata explain fake.database` renders that schema. It needs no project file: infrata loads the
plugin named by the type's prefix.

```
$ infrata explain fake.database
fake.database
  A fake database. Requires a network.

Required:
  engine           string   Database engine  (replaces on change)

Optional:
  network          string   Network this database sits in
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
project (`README.md:67-68`).

### `Computed`: the cloud assigns it

Mark an attribute `Computed` when your cloud decides its value: an ID, an IP address, an endpoint, a
generated password. Configuration may not set it. A user who tries gets "is computed and cannot be
set" (`internal/compiler/schema.go:82`). Before the resource exists, a plan shows the value as
unknown (`internal/planner/diff.go:85`):

```
      endpoint: (known after apply)
```

(`README.md:82`.) Another resource can still reference it: `database_url: ${db.endpoint}` plans as
`(known after apply)` too (`README.md:75`), and is resolved at apply time.

A computed attribute may not also be `Required` or have a `Default`. `Definition.Validate` refuses
both combinations (`pkg/schema/definition.go:94-99`).

### `ForceNew`: changing it replaces the resource

Mark an attribute `ForceNew` when your API cannot change it in place: a server's image, a
database's engine, a network's CIDR. A change to it plans as **replace** instead of **update**, and
the plan names the attribute that forced it (`internal/planner/render.go:69-72`). This is the
README's drift example: someone edited `engine` outside infrata.

```
  -/+ fake.database.db  (replacement forced by: engine)
    ⚠ This resource has 1 dependent resource.
      endpoint: "db-2.db.fake" -> (known after apply)
      engine: "mysql" -> "postgres"
```

(`README.md:196-199`.) Get `ForceNew` right. A user approves a plan largely on the difference
between *update* and *destroy and recreate*. If a field is missing `ForceNew` when it should have
it, the plan proposes an update your API then rejects. If a field has `ForceNew` when it shouldn't,
every edit to it proposes destroying real infrastructure.

### `Sensitive`: it is a secret

Mark passwords, tokens and private keys `Sensitive`. Their values are shown as `<sensitive>`
(`pkg/value/format.go:15`):

```
      password: <sensitive>
```

(`README.md:84`.) You do not need to mark individual values you return. The host forces the flag
from the schema onto every value it receives (`adapter.go:348-353`), and
`internal/fake/protocol_test.go:84-104` is a test of that: the fake marks nothing, and the host
still redacts. What the host cannot catch is a **schema** that forgets the flag, because the schema
is the only thing that knows which attributes are secret.

### `Default`: a datum

A default is a plain Go value of the attribute's declared `Kind`: `int64(10)`, `"gp3"`, `true`.
The compiler fills it in when configuration omits the attribute, and the plan marks where it came
from (`pkg/value/format.go:251-258`):

```
      size: 10 [default, from provider default]
```

(`README.md:85`.) A default of the wrong kind stops the plugin from loading. It fails as the schema
is encoded (`pkg/schema/wire.go:61-67`). Note `int64(10)`, not `10`: an untyped `10` is an `int`,
which `schema.DatumValue` accepts (`attribute.go:69-70`), but writing `int64` says what you mean.

**Why a datum and not a function?** The field used to be a function of environment, region,
account and project. A function cannot cross a pipe, and the one use it had was withdrawn
(`pkg/schema/attribute.go:26-39`). A value that really does vary by region or account is one of two
other things. Either it is the user's choice, in which case make it a variable they can see in
configuration, or your cloud decides it, in which case make it `Computed` and report it.

### The `Update` contract

`Update` receives the resource as it is (`current`) and as configuration wants it (`desired`). Make
the resource match `desired`. That includes **removing** anything `desired` no longer has.

Merging instead is the easy mistake. If a user deletes `tags:` from configuration, a merging `Update`
reports success but leaves the tags in place, and every later plan proposes removing them again,
forever. The one exception: `desired` never contains computed attributes, so a remove-everything-
not-in-desired loop must keep those:

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
an optional attribute converges" (`e2e/e2e_test.go:203-213`).

---

## 4. Requirements

A `Requirement` says what a resource needs in order to exist, for example "a database must sit
inside a network". Declaring one gives your users missing-dependency detection. The compiler checks
it before any provider is called, and reports what is missing and how to fix it
(`internal/compiler/validate.go:113-156`). A project that declares a `fake.database` and no network:

```
$ infrata plan dev
Error: "db" is missing required network
  at infra.yml:7:3

  A database must sit inside a network
  Satisfied by a resource of type: fake.network

  Suggested action:
    Add a resource of type fake.network to this configuration.
Error: configuration is not valid
```

Without the requirement, that project would plan cleanly and the user would find out when your
cloud's API rejected the create, halfway through an apply. `explain` lists requirements under
`Requires:` (`internal/cli/explain.go:89-94`), shown in the output in section 3.

Know what a requirement checks. It is satisfied if **any** resource of a satisfying type exists
anywhere in the configuration. It is not a check that this resource references that one, because a
`Requirement` names no attribute to trace (`validate.go:119-125`). The actual dependency, and so
the order resources are created in, still comes from the reference the user writes, such as
`network: ${network.id}`. `Optional: true` records a requirement without enforcing it
(`validate.go:144`).

---

## 5. Errors and retries

When a call fails, infrata asks your plugin how dangerous it would be to try again. You classify.
infrata decides whether to retry and how long to wait (`pkg/provider/provider.go:52-60`). There are
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
cloud. The retry rules belong to infrata, and a later version may treat the classes differently.

### Why `NotSafeToRetry` is the default

It is the zero value of `provider.Retryability` (`pkg/provider/provider.go:57`). The protocol treats
a missing classification as `NotSafeToRetry` (`pkg/pluginproto/proto.go:97-99`). The two ways to get
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
`adapter.go:142-147`). `internal/fake/protocol_test.go:106-130` checks that a classification
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
the host never retries it. Because infrata writes state as each operation finishes, everything that
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

`internal/fake/inject_test.go:325-349` checks that a create cancelled during the delay returns
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

`plugintest.Open` runs your plugin on one end of an in-memory pipe and infrata's real host on the
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

The in-memory pipe is `internal/pluginhost.InProcess` (`internal/pluginhost/connect.go:28-58`), the
same path infrata uses to serve its transitional built-in fake provider when no binary is found
(`internal/pluginhost/loader.go:118-123`). `pkg/plugintest` exists
because a package under `internal/` can't be imported by another module (`plugintest.go:22-24`).

**Know which layer to assert at.** Don't write a layer-1 test that asserts your `Provider` marks
sensitive values or carries bookkeeping forward. It shouldn't do either, so that test would pin
behaviour you are supposed to leave out. Assert instead, through `plugintest`, that the *host* does
it. The fake has both halves: `internal/fake/provider_test.go:284-299` asserts the plugin leaves
bookkeeping **unset**, and `internal/fake/protocol_test.go:52-82` and `:84-104` assert the host
re-attaches bookkeeping and redacts a discovered password.

### Layer 3: the binary, once, behind a build tag

One suite builds real binaries and runs a real `infrata` against your plugin. That proves the
packaging: the binary name, the search path, the handshake, and real output. `e2e/e2e_test.go` is
behind `//go:build e2e` (`e2e/e2e_test.go:1`) because it builds the `infrata` CLI from source, which is
slow. Without the tag, `go test ./...` never builds it. Run the suite with
`go test -tags e2e -count=1 ./e2e/`. It looks for the infrata source in `INFRATA_SRC`, or next to this
repository by default, and skips if the source isn't there (`e2e/e2e_test.go:32-42`).

Its main test walks the whole workflow against one project (`e2e/e2e_test.go:168-252`): explain,
plan, apply (with a clean re-plan), a hand edit planning as a forced replacement and its repair, a
removed optional attribute converging, an injected failure failing the apply once, removing a
resource destroying it, discover plus import adopting what infrata did not create, and finally
destroy emptying the cloud. Its projects are
`e2e/testdata/basic/infra.yml` and `e2e/testdata/instances/infra.yml`. The README quotes the first
byte for byte, and `internal/fake/readme_test.go` fails if they drift apart.

Keep this layer small. Each case costs a full process launch, and a failure here tells you less about
where the bug is than the same failure at layer 1 or 2.

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

Most of this section is recommendation, not host behaviour. infrata has no credential mechanism of
its own. Your plugin gets its configuration and its environment, and nothing else.

**Take credentials the way your cloud's own tooling does**: its standard environment variables and
config files. The plugin process inherits infrata's environment (`internal/pluginhost/connect.go:67-68`).
A user who can already use your cloud's CLI shouldn't have to configure anything twice.

**Accept explicit configuration in `providers:` as an override**, for users with more than one
account. Remember that a value there reaches `New` *resolved* (`pkg/provider/provider.go:33-35`). It
may come from a variable, so it can differ between environments, and the same `providers:` entry
may mean a different account in `staging` and `prod`. Resolve credentials inside `New`, not at
package init.

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
$ infrata plan dev
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
panic puts the token in the failure message. infrata's redaction works on values that travel through
infrata marked sensitive (`pkg/value/format.go:15`). It cannot redact text your plugin prints itself.
The same goes for error messages: don't format a credential into an `error`, because its text is what
the user sees.

---

## 9. What the host enforces, so you don't

Several guarantees used to be rules in a doc comment that every provider had to remember. A binary
someone else built can't be held to a comment, so infrata's host enforces them for every plugin
(`PLAN.md` §31.1, "What the engine stops trusting a plugin with").

**Do not reimplement any of these.** It isn't only wasted effort. A plugin that also does the host's
job has tests that keep passing when the host's check is broken, and so it hides the host bug it was
supposed to guard against.

1. **Bookkeeping is never sent, so it can't be dropped.** Dependencies, lifecycle and timestamps never
   reach the plugin, and are re-attached to results (`adapter.go:303-318`). *Prevents:* losing
   `Lifecycle`, which makes a `prevent_destroy` guard vanish with no error, and losing
   `Dependencies`, which is the only destroy-ordering information once a resource has left
   configuration.
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
   (`adapter.go:72-74`, `pkg/schema/definition.go:79-112`), against the type-prefix rule
   (`adapter.go:79-85`), and against the reserved attribute names `prevent_destroy` and `retain`
   (`adapter.go:86-96`, `internal/registry/registry.go:291`). The registry then applies its own
   checks to the loaded plugin (`internal/registry/registry.go:75-95`): no type in the `module.`
   namespace, and no type already claimed by another plugin (`registry.go:246-265`). A default of the
   wrong kind fails when the schema is encoded (`pkg/schema/wire.go:61-67`). *Prevents:* two plugins
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
9. **The retry and backoff policy belongs to infrata** (`internal/executor/retry.go`). *Prevents:*
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
      -ldflags "-X github.com/infrata/infrata-provider-fake/internal/fake.Version=${version}" \
      -o "$work/$stem/$name" ./cmd/infrata-plugin-fake
```

Section 13 explains why the default must never equal the version in `plugin.yaml`.

### Building and naming

A plugin is a plain Go binary, so building for another platform is only `GOOS` and `GOARCH`.
`CGO_ENABLED=0` gives a static binary that doesn't depend on the target's libc
(`scripts/build-release:34-36`). The executable inside the archive must be named
`infrata-plugin-<name>`, with `.exe` for Windows (`scripts/build-release:25-26`), because that is the
filename infrata searches for (`internal/pluginhost/connect.go:150-155`). Archive naming is covered in
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
    loaded from: …/bin/infrata-plugin-fake

  Suggested action:
    Install a version matching >= 1.0.0, or widen the constraint once you have confirmed this one works.
```

Only a plugin reporting exactly `0.0.0` gets the separate message "does not report a version"
(`loader.go:157`, `errors.go:145-151`). Two more rules, both from `PLAN.md` §31.1:

- **A constraint naming a plugin the project doesn't use is an error**, because it would pin nothing
  (`internal/providers/prepare.go:169-194`).
- **There is one version per plugin, not per instance.** `plugins:` is keyed by plugin because every
  instance of a plugin shares one process (`PLAN.md` §31.1).

Don't confuse this with a project's `infrata:` floor, the constraint on infrata itself. That check
*exempts* development builds of infrata (`internal/compiler/compile.go:231-269`). `plugins:` does not
exempt development builds of your plugin, because for the user it is a third-party binary they chose
to install, and they can act on the complaint (`loader.go:136-140`).

### The protocol version is the compatibility contract

What must stay compatible between infrata and your plugin is the **wire protocol**, not the Go types
you compiled against (`pkg/pluginproto/proto.go:9-12`). The host accepts a *set* of protocol versions
(`proto.go:25-37`). A plugin built against an older SDK keeps working as long as its protocol version
is in that set, so you don't have to rebuild for every infrata release (`PLAN.md` §61.3). Under
infrata's own versioning rules, a minor release may add a protocol version but must keep the previous
one, and only a major release may drop one (`PLAN.md` §61.1). If the set no longer includes your
version, the user gets an error naming your plugin, its path, both sides' versions, and which one to
upgrade (`internal/pluginhost/errors.go:23-46`).

---

## 11. Depending on infrata today

`github.com/infrata/infrata` is not yet published as a module you can fetch. Until it is, build
against a checkout with a `replace` directive:

```
// go.mod:10
replace github.com/infrata/infrata => ../ilan
```

This repository expects infrata checked out next to it as `../ilan`. The release workflow reproduces
that layout by checking out both repositories side by side (`.github/workflows/release.yml:22-32`).
Nothing else is needed: the SDK and protocol use only the standard library, so a plugin gains no
third-party dependency from them (`PLAN.md` §31.1). When infrata is published, delete the `replace`
line and require a real version.

Your module's own `go` directive must be at least infrata's: `go 1.27.0` as of 2026-09-13 (infrata's
`go.mod:15`, and this repository's `go.mod:3`). If it's lower, the build fails with Go's ordinary
"requires go >= …" error, which names the module whose requirement it is. If that module is
infrata, raise your `go` line to match infrata's `go.mod`.

---

## 12. The manifest, `plugin.yaml`

Every plugin repository ships a `plugin.yaml` at its root that says what the plugin is and what it
works with (`PLAN.md` §31.2). This repository's:

```yaml
# plugin.yaml
# plugin.yaml: what this plugin is, and what it works with. infrata PLAN.md §31.2.
# Read at a release TAG, never at the default branch, which describes unreleased code.
manifest: 1
name: fake
version: 0.1.1
protocol: [1]
platforms: [linux/amd64, linux/arm64, linux/arm, linux/386, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64]
description: A fake provider for testing infrata without a cloud account.
source: https://github.com/infrata/infrata-provider-fake
```

No infrata code reads this file yet. It is written for `infrata plugins install`, which is planned
but not built (`PLAN.md` §31.2, "Where infrata reads it"). Ship it anyway. Your release gate checks
against it (section 13). And once install exists, it will read the manifest at each release tag, so a
release tagged without one stays without one. The plan is to install such a plugin with only a
warning (`PLAN.md` §31.2), but then none of the compatibility checks below apply to it.

### Its shape follows from its purpose

The manifest is fetched over the network and read **before any binary is downloaded**, so a search
can answer "is this compatible with the infrata I'm running, and is there a build for my machine?"
without downloading anything else. It will be read by infrata builds released for years afterwards.
Every design decision below follows from those two facts.

| Key | Required | Meaning |
| --- | --- | --- |
| `manifest` | yes | The format version of this file. Checked first, before any other key. |
| `name` | yes | The plugin's name: binary `infrata-plugin-<name>`, `Plugin.Name()`, and every type's prefix. |
| `version` | yes | `MAJOR.MINOR.PATCH`. Must equal the tag the file is read at. |
| `protocol` | yes | A **list** of every protocol version the plugin speaks, because the host accepts a set. |
| `platforms` | yes | `GOOS/GOARCH` for every build you publish. |
| `description` | yes | One line, for a search result. |
| `infrata` | no | The infrata releases this plugin is known to work with, in `pkg/semver` syntax. |
| `source` | no | Where the plugin lives, for a search result to link to. |

(`PLAN.md` §31.2, the key table.)

Validate your own manifest with `pkg/pluginmanifest.Parse` (`ilan/pkg/pluginmanifest/manifest.go`'s
`Parse`) — the same parser `infrata plugins install` will use — rather than a hand check a typo
could pass.

### Why it is read at a release tag

The file on your default branch describes **unreleased** code. Reading it to judge `v1.2.0` answers
the wrong question once `main` has moved on to `v1.3.0`. That is also the mistake an implementer makes
by default, because the `HEAD` URL is the obvious one. The manifest holds two kinds of information:
identity (`name`, `description`, `source`), which is the same on every ref, and the compatibility of
one version (`version`, `protocol`, `platforms`, `infrata`), which is not. One file can serve both
only because it is always read at a tag (`PLAN.md` §31.2, "READ IT AT THE TAG").

### Why the format is versioned when infrata's configuration is not

infrata's configuration language deliberately has no version number. It rejects unknown keys, so an
older infrata that meets newer syntax stops and names the key it didn't understand (`PLAN.md` §61.2).
That works because configuration is written and read by the same person, at the same time, on one
machine.

A manifest is different. A plugin author writes it, and infrata builds read it for years afterwards
with no way to upgrade the reader in step. If every reader rejected unknown keys, a 2026 infrata could
never install a 2027 plugin. So readers reject unknown keys only for a `manifest` version they know,
and tolerate them with a warning for a version they don't (`PLAN.md` §31.2, "Why the format is
versioned").

### Why `infrata` is optional

A missing `infrata` key means **unconstrained**. Don't write `infrata: ">= 0.0.0"` to say "works with
anything". It is a real constraint that means something subtly different, and it adds noise. State a
floor only when you know of a release your plugin does not work with. When one is stated, a
development build of infrata is exempt, as it is from a project's own floor (`PLAN.md` §31.2,
"Compatible means three things"). Validate your `infrata:` string with `semver.ParseConstraint`
(`pkg/semver/semver.go:126`) so you use the same parser infrata will.

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
publish unless they do (`PLAN.md` §31.2, "What a plugin repository owes its own manifest").

### Why a release step, not a test

You could write a unit test comparing `plugin.yaml` to a constant in the code. That is weaker in two
ways. Someone can delete or skip a test, but a failing release step blocks the release. And a test
checks source code, not the binary: it can't catch a build whose `-ldflags` stamp went wrong. The gate
here runs as the first step of `.github/workflows/release.yml` (`release.yml:38-40`), before tests,
builds or publishing.

### Why `Version()` defaults to `0.0.0-dev`

Stamping at release time only proves something if an unstamped build reports a **different** value.
Suppose `Version` defaulted to `"0.1.1"`, the same as `plugin.yaml`. Then a release whose `-X` flag
named the wrong symbol would still report `0.1.1`, correct by coincidence, and the gate would pass.
Go's linker ignores an `-X` for a symbol that doesn't exist, without any error
(`scripts/release-check:6-7`). With the default at `0.0.0-dev`, a broken stamp shows up as a
mismatch. `scripts/scripts_test.go:93-104` points `-X` at a nonexistent variable and checks that the
gate refuses, reporting `0.0.0-dev`. It also gives bug reports honest versions: a build from a
checkout never claims to be a release (`internal/fake/plugin.go:15-20`).

### How `scripts/release-check` reads the version

It needs no infrata at all. It builds the binary with the same stamp a release uses, then runs it
with the cookie set and an empty stdin. The SDK writes the handshake and exits, and the script pulls
out the version field:

```bash
# scripts/release-check:37-44
CGO_ENABLED=0 go build -trimpath -ldflags "-X ${symbol}=${version}" -o "$work/infrata-plugin-fake" ./cmd/infrata-plugin-fake

# The binary refuses to start without the host's cookie. With it and an empty stdin, it writes
# its handshake {"protocol","name","version"} and exits. Captured whole, not piped to head,
# so pipefail cannot turn an early-closed pipe into a false failure.
out="$(INFRATA_PLUGIN_COOKIE=release-check "$work/infrata-plugin-fake" </dev/null 2>/dev/null)"
handshake="${out%%$'\n'*}"
reported="$(printf '%s' "$handshake" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
```

Before that, it checks the tag has the form `vMAJOR.MINOR.PATCH` (`release-check:14-19`) and that the
tag agrees with `plugin.yaml`'s `version`, tolerating CRLF line endings (`release-check:24-32`). Each
failure is tested in `scripts/scripts_test.go`.

### Archive names and `SHA256SUMS`

`infrata plugins install` will build the download name from a convention rather than read it from
anywhere (`PLAN.md` §31.2):

```
infrata-plugin-<name>_<version>_<goos>_<goarch>.tar.gz      (.zip for windows)
```

`scripts/build-release` builds every platform `plugin.yaml` lists (`build-release:16`). It archives
each build under that name, with the binary, `plugin.yaml` and `README.md` inside a top-level
directory of the same stem (`build-release:29-46`). `scripts/scripts_test.go:106-147` checks the names
and contents. A wrongly named archive is a release nobody can install.

After the build, the workflow checksums every archive into a `SHA256SUMS` release asset
(`.github/workflows/release.yml:55-57`), and then publishes (`release.yml:59-63`). The checksums live
in the release, not the manifest, because they don't exist until the build does.

To release: bump `version` in `plugin.yaml`, commit, tag `v<that version>`, and push the tag. If the
tag, manifest or binary disagree, the workflow stops before publishing anything.
