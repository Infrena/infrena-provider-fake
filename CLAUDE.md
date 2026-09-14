# CLAUDE.md

> Project notes (source of truth): Obsidian Vault/projects/labs/infra-tool.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

## What this repository is

`infrena-provider-fake` is the **fake provider for [infrena](https://github.com/infrena/infrena),
distributed as a plugin binary**: `infrena-plugin-fake`.

Infrena is a declarative infrastructure CLI. A provider plugin is a separate executable that
infrena launches as a child process and talks to over stdin/stdout in newline-delimited JSON. This
repository builds one.

**Nothing here touches a network.** The fake provider's "cloud" is a hand-editable JSON file on
disk. That is the whole point of it: it makes infrena's entire engine — planning, applying, drift
detection, import, dependency ordering, failure injection — testable without cloud credentials.

### Two jobs, and the second one is why this repo exists separately

1. **Replace the in-process fake provider.** Infrena currently carries `providers/test/` inside its
   own module, wired in by direct import. This repository replaces it with a real plugin.
2. **Be the worked example every plugin author reads.** It is the only plugin whose source anyone
   can study, so it is also the reference implementation and the documentation. A third-party author
   building `infrena-plugin-hetzner` should be able to follow this repository and succeed.

The second job is the reason the fake provider moved out of infrena's tree rather than becoming
`cmd/infrena-plugin-test/` inside it. A plugin that lives in the engine's own module can quietly
depend on something an external author cannot have — an internal package, a test helper, a shared
fixture — and nobody would notice until the first outside plugin failed. Here, if it compiles, the
dependency is one an outside author has too.

## Current state

**Built and passing.** The plugin (`internal/fake/`: cloud file, schemas, CRUD with attribute
removal, discover/import, failure/latency injection, a per-file lock, plugin configuration) and its
binary (`cmd/infrena-plugin-fake/`) are complete and documented, with a version-gated release
workflow that has published v0.1.0, v0.1.1 and v0.2.0. Those three predate the rename and carry the
old names: v0.2.0 was built against infrata v0.3.0, speaks protocol 2, is named
`infrata-plugin-fake`, was the first cut through ci.yml's gate, and pairs only with infrata ≤ v0.3.0.

**Renamed (2026-09-14).** Infrata was renamed Infrena (a legal name collision): module
`github.com/infrena/infrena-provider-fake`, binary `infrena-plugin-fake`, `INFRENA_*` variables, and
`manifest: 2` with `infrena: ">= 0.4.0"`. `go.mod` requires infrena v0.4.0 (`e2be8bf`), the first
release under `github.com/infrena/infrena`; tags v0.1.0–v0.3.0 declare the old path and cannot satisfy
it, and go.sum carries the v0.4.0 hashes. No release of this plugin has been cut under the new name.
CI uses the `INFRENA_CHECKOUT_TOKEN` secret; the old `INFRATA_CHECKOUT_TOKEN` is being retired. Before
the rename it proved the path (ci.yml's first GitHub run 34797363934 green on both jobs; the release
path's `secrets: inherit` proven by the v0.2.0 release run 34848477674).

CI (`.github/workflows/ci.yml`, on push to `main`, pull requests, and called by `release.yml`) builds
against the infrena RELEASE `go.mod` requires in its gating `tag` job, and against infrena `main` in
an advisory `main` job. The implementation followed
`docs/plans/2026-09-13-port-fake-provider.md`; read that plan (including its verification log and
the ledger it summarizes) before touching this repository's design, rather than re-planning from
scratch.

Two test suites, both green with `-count=1`:

```bash
go test -count=1 ./...                    # the plain suite: unit + pkg/plugintest protocol
                                           # tests, plus scripts/ (release-check / build-release)
go test -tags e2e -count=1 -v ./e2e/      # compliance suite against a real infrena binary
```

`go test -count=1 ./...` already covers `scripts/` — there is no separate third layer to run for
it. The `-tags e2e` suite builds `infrena` from a sibling checkout — `$INFRENA_SRC`, default
`../infrena` — and drives it as a subprocess; it needs that checkout present and buildable, and is
slower than the plain suite, so it is not part of the default `go test ./...` run. (`$INFRENA_SRC`
only chooses which infrena the CLI is built from for this suite — locally the plugin itself still
compiles against `../infrena` through `go.mod`'s `replace`, so pointing `INFRENA_SRC` at a different
checkout pairs a host built from one infrena with an SDK compiled against another. CI's tag job avoids
that: it drops the replace and builds the host from the same tag.)

Release plumbing: `plugin.yaml` (infrena `PLAN.md` §31.2, `manifest: 2`; its `infrena: ">= 0.4.0"` is
the first renamed release, which `go.mod`'s require matches and which is not enforced
at runtime yet — `internal/fake/manifest_test.go` checks it refuses 0.3.x and admits 0.4.0 — and its
`protocol: [2]` is exactly the `pluginproto.Version`
that require's SDK speaks — the amended §31.2 defines `protocol:` as what this release's binary
speaks, so it changes in the same commit as the require, and `internal/fake/manifest_test.go` and
`scripts/release-check` both refuse a mismatch), `scripts/release-check`, `scripts/build-release`,
`scripts/ci-use-infrena-tag`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`.

**go.sum carries infrena's hashes on purpose** (the infrata-path lines were removed in the rename). CI's tag job builds under `-mod=readonly`
with the replace dropped, so it needs them, and a local `go mod tidy` (replace present) strips them.
`TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds` fails when that happens; restore with
`scripts/ci-use-infrena-tag sum`, and run the same after bumping the infrena require (then raise
`plugin.yaml`'s `infrena:` floor to match, and set its `protocol:` to that release's
`pluginproto.Version`). `Version` defaults to `"0.0.0-dev"` and is
stamped only by `-ldflags` at release (Ruling R9), so an unstamped build cannot silently satisfy a
project's `plugins:` constraint or the release gate.

**Known limits:**
- The `test.*` → `fake.*` rename (D1) is migrated by infrena, not here: state version 1 → 2
  (infrena `internal/state/migrations.go`) rewrites the types and the implicit `test` instance
  name. This plugin itself carries no migration code. Renaming the type prefix from `test.` to
  `fake.` would otherwise break any project or state file using infrena's old in-tree provider,
  which infrena no longer ships.
- Symlinked paths to the same cloud file are not unified into one lock (D5) — two different paths
  naming the same file on disk can still race.
- `go.mod` carries `replace github.com/infrena/infrena => ../infrena` (D7) for local work, and will for
  as long as infrena stays private (until it is feature complete, infrena PLAN.md §31.1); a sibling
  checkout named `infrena` — what `git clone` creates — is required to build or test locally, and a
  local result reflects that checkout, not the required release. CI drops the replace
  (`scripts/ci-use-infrena-tag use`), which needs `GOPRIVATE`, the token, and the committed go.sum hashes.
- Nothing here depends on the old `../infrata` path or names (verified with fresh clones and no
  `infrata` directory present).
- Because infrena is private, `GOPRIVATE` also bypasses the checksum database: the committed go.sum
  hash is the only thing that would notice a pinned tag being moved.

## Where the contract lives

The protocol and the interfaces are defined in the infrena repository, not here:

| What | Where | Read it for |
| --- | --- | --- |
| `PLAN.md` §31.1 | infrena repo | the design, the alternatives rejected, and what the host refuses to trust a plugin with |
| `PLAN.md` §31.2 | infrena repo | **the agreed `plugin.yaml` manifest** — the schema this repository ships, and why it is read at the git tag |
| `PLAN.md` §61 | infrena repo | versioning: the product semver, the format versions, and why the config language is not versioned |
| `pkg/pluginproto` | infrena repo | the wire messages and the protocol version |
| `pkg/pluginsdk` | infrena repo | `Main(p)` — the whole of a plugin's `main()` |
| `pkg/provider` | infrena repo | `Plugin` and `Provider`, the two interfaces to implement |
| `pkg/schema` | infrena repo | how to describe resource types |
| `pkg/value` | infrena repo | the value model, including per-leaf sensitivity |
| `pkg/semver` | infrena repo | the version-constraint syntax, for validating this plugin's own `infrena:` field |
| `pkg/plugintest` | infrena repo | the in-process harness this repository's protocol tests use |

**Read `PLAN.md` §31.1 and §31.2 before writing any code.** It is the specification this repository
implements, and it records decisions with their reasoning — including several things deliberately
NOT delegated to plugins, which you must not reimplement here.

`AGENT.md` in this repository is the authoring guide, written to be portable to any plugin. Read it
second. It holds the API surface, the rules, and the failure modes.

## Stack and commands

Go 1.27. Infrena's `go.mod` declares that floor as of 2026-09-13, so this module must declare it
too or it will not build against `../infrena`. The error Go gives for a too-low `go` directive DOES
name the module whose requirement it is (`go: <module>@<version> requires go >= X (running go Y;
…)`) — with `GOTOOLCHAIN=auto` (this repo's default), Go instead fetches a newer toolchain
silently; the named-module error only surfaces under a pinned `GOTOOLCHAIN=local` on a too-old
toolchain.

**Standard library plus `github.com/infrena/infrena` only.** The protocol deliberately adds no
third-party dependency, and a fake provider that needed one would be evidence of a problem in the
design rather than in this repository. `go.mod`'s `gopkg.in/yaml.v3 // indirect` arrives through
infrena's `pkg/pluginmanifest`, not through anything imported here.

```bash
go build ./cmd/infrena-plugin-fake   # build the plugin
go test -count=1 ./...               # the suite; -count=1 is mandatory, cached results hide fixture edits
go vet ./...
gofmt -l .
```

## How to plan this work

Already done. `docs/plans/2026-09-13-port-fake-provider.md` records the decisions (why `fake.*` not
`test.*`, why `Update` deletes attributes the desired state omits, why the lock is per cloud file,
why the manifest and release gate exist), the behaviour ledger of what ported unchanged, changed, or
was deliberately dropped from infrena's in-tree `providers/test`, and a verification log of every
factual claim checked against infrena's code — including the ones that came back different from
what was assumed. Read it before planning new work here, rather than re-deriving any of this from
scratch; the sections below on "Documentation this repository must ship" and "Rules for code in this
repository" still apply to any change.

## Documentation this repository must ship

These are deliverables, not follow-ups. The repository is not finished without them.

### `README.md`

For someone who has just found this repository. It must cover:

- what the fake provider is for, in two sentences, including "no network, no credentials"
- how to build it and where to put the binary so infrena finds it (the search path is in
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
whether the awkwardness is really a defect in infrena's SDK that should be fixed there instead.

### `docs/writing-a-provider.md`

The long-form guide for a developer building a plugin for a real cloud, where `AGENT.md` is the
condensed reference. It must cover at least:

- the two interfaces, method by method, with what the host does to each result
- how to model a resource type: when an attribute is `Computed`, when it is `ForceNew`, when it is
  `Sensitive`, and what each one causes downstream in a plan
- `Requirements`, and what missing-dependency detection gives a user
- error classification, and the difference between the three retryability levels in terms of what
  the executor will do
- how to test a plugin without a cloud account, including what infrena's own `pkg/plugintest`
  (`Open`/`Configure`) makes possible
- credentials: how a plugin should take them, and why it must never log them
- the things the host enforces so a plugin does not have to, so an author does not waste effort
  reimplementing them
- how to version and release a plugin, and how a project constrains a version with `plugins:`

Write it for a competent Go developer who knows their cloud's API and knows nothing about infrena.

## Rules for code in this repository

These are the ones that are easy to get wrong and expensive to get wrong.

- **stdout is the protocol. Never print to it.** One stray `fmt.Println` corrupts the stream and
  every subsequent message; the symptom is an unrelated parse error much later. Log to stderr,
  which infrena prefixes with the plugin name and shows under `--verbose`. The SDK redirects
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
