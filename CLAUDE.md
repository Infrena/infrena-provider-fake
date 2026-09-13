# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

## What this repository is

`infrata-provider-fake` is the **fake provider for [infrata](https://github.com/infrata/infrata),
distributed as a plugin binary**: `infrata-plugin-fake`.

Infrata is a declarative infrastructure CLI. A provider plugin is a separate executable that
infrata launches as a child process and talks to over stdin/stdout in newline-delimited JSON. This
repository builds one.

**Nothing here touches a network.** The fake provider's "cloud" is a hand-editable JSON file on
disk. That is the whole point of it: it makes infrata's entire engine — planning, applying, drift
detection, import, dependency ordering, failure injection — testable without cloud credentials.

### Two jobs, and the second one is why this repo exists separately

1. **Replace the in-process fake provider.** Infrata currently carries `providers/test/` inside its
   own module, wired in by direct import. This repository replaces it with a real plugin.
2. **Be the worked example every plugin author reads.** It is the only plugin whose source anyone
   can study, so it is also the reference implementation and the documentation. A third-party author
   building `infrata-plugin-hetzner` should be able to follow this repository and succeed.

The second job is the reason the fake provider moved out of infrata's tree rather than becoming
`cmd/infrata-plugin-test/` inside it. A plugin that lives in the engine's own module can quietly
depend on something an external author cannot have — an internal package, a test helper, a shared
fixture — and nobody would notice until the first outside plugin failed. Here, if it compiles, the
dependency is one an outside author has too.

## Current state

**Nothing is built yet.** This repository contains planning and authoring documentation only. See
"How to plan this work" below; that is the first task.

## Where the contract lives

The protocol and the interfaces are defined in the infrata repository, not here:

| What | Where | Read it for |
| --- | --- | --- |
| `PLAN.md` §31.1 | infrata repo | the design, the alternatives rejected, and what the host refuses to trust a plugin with |
| `PLAN.md` §31.2 | infrata repo | **the agreed `plugin.yaml` manifest** — the schema this repository ships, and why it is read at the git tag |
| `PLAN.md` §61 | infrata repo | versioning: the product semver, the format versions, and why the config language is not versioned |
| `pkg/pluginproto` | infrata repo | the wire messages and the protocol version |
| `pkg/pluginsdk` | infrata repo | `Main(p)` — the whole of a plugin's `main()` |
| `pkg/provider` | infrata repo | `Plugin` and `Provider`, the two interfaces to implement |
| `pkg/schema` | infrata repo | how to describe resource types |
| `pkg/value` | infrata repo | the value model, including per-leaf sensitivity |
| `pkg/semver` | infrata repo | the version-constraint syntax, for validating this plugin's own `infrata:` field |
| `pkg/plugintest` | infrata repo | the in-process harness this repository's protocol tests use |

**Read `PLAN.md` §31.1 and §31.2 before writing any code.** It is the specification this repository
implements, and it records decisions with their reasoning — including several things deliberately
NOT delegated to plugins, which you must not reimplement here.

`AGENT.md` in this repository is the authoring guide, written to be portable to any plugin. Read it
second. It holds the API surface, the rules, and the failure modes.

## Stack and commands

Go 1.27. Infrata's `go.mod` declares that floor as of 2026-09-13, so this module must declare it
too or it will not build against `../ilan` — and the error Go gives for a too-low `go` directive
does not name the dependency that caused it.

**Standard library plus `github.com/infrata/infrata` only.** The protocol deliberately adds no
third-party dependency, and a fake provider that needed one would be evidence of a problem in the
design rather than in this repository.

```bash
go build ./cmd/infrata-plugin-fake   # build the plugin
go test ./...                        # the suite
go test -count=1 ./...               # -count=1 is mandatory; cached results hide fixture edits
go vet ./...
gofmt -l .
```

## How to plan this work

Do not start writing code. Plan first, and plan against what already exists.

### 1. Read the existing implementation

`providers/test/` in the infrata repository is a complete, working fake provider with years of
decisions baked into it. This repository is a **port**, not a rewrite. Read all of it before
planning:

- `definitions.go` — three resource types (`test.network`, `test.database`, `test.application`)
  with a dependency chain between them, a computed attribute on each, a sensitive attribute, a
  composite (map) attribute, and static defaults.
- `cloud.go` — the hand-editable JSON cloud file, its shape, and its atomic write.
- `provider.go` — the `Provider` implementation, including `carryForward`, drift, failure
  injection and latency simulation.

For each behaviour you find, decide and write down: **does it port unchanged, does it change
because the host now enforces it, or does it disappear?** The third category is real: several
things `providers/test` does carefully are now the host's job, and duplicating them here would be
worse than useless — it would hide a host bug behind a plugin working around it.

Start that list from `PLAN.md` §31.1's "What the engine stops trusting a plugin with". Every item
there is something to **delete** on the way in, not port.

### 2. Decide the resource type names

The host requires a plugin's types to be prefixed with the plugin's own name. This plugin is
called `fake`, so it serves `fake.*` — and the existing types are `test.*`.

**That rename is a breaking change to every project and state file that uses the fake provider.**
It is also unavoidable: `test.network` from a plugin named `fake` is refused on load. Plan it
explicitly. Decide, and record in the plan:

- whether the plugin should instead be named `test`, keeping `test.*` and breaking nothing
- what happens to an existing state file naming `test.network` if the types are renamed
- which choice is better for the SECOND job above — the name a reader of this repository will
  copy

There is a real argument each way. Make the argument, pick one, and say what it costs. Do not
default to whichever is less typing.

### 3. Write the plan

Use the `superpowers:writing-plans` skill if it is available. Save it to
`docs/plans/YYYY-MM-DD-<name>.md`. The plan must produce working, testable software at each step,
and every step must name the files it touches and the test that proves it.

Suggested shape, to be argued with rather than followed:

1. `go.mod`, `cmd/infrata-plugin-fake/main.go` calling `pluginsdk.Main`, and the schemas. Verify by
   launching the binary from infrata and running `infrata explain fake.network`.
2. The cloud file: load, save, atomic write.
3. `Read`, `Create`, `Update`, `Delete`. Verify with a real `plan` → `apply` → re-plan clean.
4. Drift: mutate the cloud file by hand, see `refresh` report it.
5. `Discover` and `Import`.
6. Failure and latency injection, which is what makes infrata's executor testable.
7. The documentation deliverables below.

### 4. Verify every claim in the plan against the code

Before committing the plan, check each factual claim in it by reading the code it describes. This
project has a history of plans whose governing claim was wrong — a plan that said a hyphen is not
an identifier character when the parser accepted hyphens, and one that said dropping a value early
would break a later feature when it would not. **A plan's confident sentence about code you have
not read is the most expensive thing in it.**

Record the verification in the plan, with its findings, including the ones that came back
different from what you assumed.

## Documentation this repository must ship

These are deliverables, not follow-ups. The repository is not finished without them.

### `README.md`

For someone who has just found this repository. It must cover:

- what the fake provider is for, in two sentences, including "no network, no credentials"
- how to build it and where to put the binary so infrata finds it (the search path is in
  `AGENT.md`)
- a copy-pasteable `infra.yml` that works, and the three commands that exercise it
- the shape of the cloud file, and the fact that editing it by hand is a supported thing to do —
  it is how you simulate drift
- every resource type, its attributes, and which are computed, sensitive or force-new
- how to inject a failure or latency, with an example
- a pointer to `AGENT.md` for anyone writing their own plugin

Show real, tested commands and real output. A README whose example does not run is worse than no
README, and the example here is the first thing anyone copies.

### `AGENT.md`

Already written. **Keep it true.** It documents the plugin API, and this repository is the thing
that proves the documentation works. If you discover while building that `AGENT.md` is wrong,
incomplete, or describes something more awkward than it needs to be, fix `AGENT.md` — and consider
whether the awkwardness is really a defect in infrata's SDK that should be fixed there instead.

### `docs/writing-a-provider.md`

The long-form guide for a developer building a plugin for a real cloud, where `AGENT.md` is the
condensed reference. It must cover at least:

- the two interfaces, method by method, with what the host does to each result
- how to model a resource type: when an attribute is `Computed`, when it is `ForceNew`, when it is
  `Sensitive`, and what each one causes downstream in a plan
- `Requirements`, and what missing-dependency detection gives a user
- error classification, and the difference between the three retryability levels in terms of what
  the executor will do
- how to test a plugin without a cloud account, including what infrata's own
  `pluginhost.InProcess` makes possible
- credentials: how a plugin should take them, and why it must never log them
- the things the host enforces so a plugin does not have to, so an author does not waste effort
  reimplementing them
- how to version and release a plugin, and how a project constrains a version with `plugins:`

Write it for a competent Go developer who knows their cloud's API and knows nothing about infrata.

## Rules for code in this repository

These are the ones that are easy to get wrong and expensive to get wrong.

- **stdout is the protocol. Never print to it.** One stray `fmt.Println` corrupts the stream and
  every subsequent message; the symptom is an unrelated parse error much later. Log to stderr,
  which infrata prefixes with the plugin name and shows under `--verbose`. The SDK redirects
  `os.Stdout` to stderr to catch this, but do not rely on it — a direct write to fd 1 still
  escapes.
- **A schema is data, and holds no functions.** It has to survive a pipe. `Default` is a datum, not
  a resolver. If you find yourself wanting a function in a schema, the answer is a computed
  attribute or a user's variable.
- **Never return `(nil, nil)` from `Create` or `Update`.** The host turns it into an error saying
  the resource may exist untracked, because that is the only honest reading. If the created state
  cannot be determined, return an error saying so.
- **Do not reimplement what the host enforces.** Sensitivity from the schema, provenance,
  bookkeeping carry-forward, undeclared-attribute rejection: all host-side. A plugin that also does
  them is a plugin whose tests pass when the host is broken.
- **A context cancellation is not a licence to abandon work in flight.** The host sends `cancel`
  and then WAITS for the real answer, because a create that already happened must be reported.
  Honour the context where it is safe to — before a mutating call, between paginated reads — and
  never in a way that leaves a real resource unreported.
- **Every test runs with `-count=1`.** Go caches test results, and a cached pass hides a fixture
  edit.
- **A fixture must contradict its expected output.** A test whose fixture would satisfy the
  assertion by accident asserts nothing. When you write a test, sabotage the code it covers and
  confirm it fails — a sabotage must leave the code COMPILING and change behaviour, because one
  that breaks the build discriminates nothing.

## Commit discipline

Stage explicit paths. `git add -A`, `git add .` and `git commit -am` are forbidden: they sweep up
scratch files, editor droppings and unrelated work, and a commit that contains something its
message does not mention is a commit nobody can review.

```bash
git add path/one path/two
git commit -m "..." -- path/one path/two
```

Write commit messages that explain why, not what. The diff already says what.
