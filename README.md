# infrata-provider-fake

The fake provider for [infrata](https://github.com/infrata/infrata), distributed as a plugin
binary: `infrata-plugin-fake`. Its "cloud" is a hand-editable JSON file on disk, so infrata's whole
engine — planning, applying, drift detection, import, and failure handling — can be exercised with
no network and no credentials.

## Build and install

This plugin requires a released infrata (`github.com/infrata/infrata v0.3.0` in `go.mod`), but a local
build uses a sibling checkout of infrata instead: infrata stays private until it is feature complete,
so fetching the module needs credentials a casual build shouldn't. Lay the two repositories out side
by side:

```
some-directory/
├── infrata/                 # infrata itself
└── infrata-provider-fake/   # this repository
```

`go.mod`'s `replace github.com/infrata/infrata => ../infrata` assumes exactly that layout. It builds
whatever that checkout holds, so check out the release `go.mod` requires (`git -C ../infrata checkout
v0.3.0`) when you want a local result that means what CI's does. Then:

```bash
go build -o infrata-plugin-fake ./cmd/infrata-plugin-fake
```

infrata looks for a plugin binary in this order, first match wins:

1. `--plugin-dir`, then `INFRATA_PLUGIN_PATH`
2. `<project>/.infra/plugins/`
3. `~/.local/share/infrata/plugins/`
4. `$PATH`

Put `infrata-plugin-fake` in one of those. `--verbose` on any infrata command prints which path each
plugin was loaded from — useful when more than one copy is on the search path.

Running the binary by hand does nothing useful and is expected to fail: it prints that it is meant
to be run by infrata as a subprocess, and exits 2. It is a protocol program, not a CLI.

## Try it

Save this as `infra.yml`:

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

This is `e2e/testdata/basic/infra.yml` in this repository, and infrata's own e2e suite applies it —
the output below is a real run, not a transcript written by hand.

```
$ infrata plan dev
Plan for project "demo", environment "dev":

  + fake.application.app
      database_url: (known after apply)
      image: "nginx:1.27"
      replicas: 1 [default, from provider default]
      url: (known after apply)

  + fake.database.db
      endpoint: (known after apply)
      engine: "postgres"
      network: (known after apply)
      password: <sensitive>
      size: 10 [default, from provider default]
      tags: {team: "data"}

  + fake.network.network
      cidr: "10.0.0.0/16"
      id: (known after apply)

Plan: 3 to create, 0 to update, 0 to replace, 0 to destroy, 0 to forget.
$ echo $?
2
```

`plan` exits 2 when it finds changes to make, and 0 when the configuration already matches what the
fake cloud holds — that is how a CI job distinguishes "nothing to do" from "review this".

```
$ infrata apply dev --auto-approve
...
Apply complete: 3 applied, 0 failed, 0 skipped.

Applied:
  + network
      cidr: "10.0.0.0/16"
      id: "net-1"
  ... (2 more resources, same shape as the plan above — trimmed here for length)
$ echo $?
2
```

`apply` also exits 2 on a successful run that changed something; a failed apply exits 1 (see
[Injecting failures and latency](#injecting-failures-and-latency)).

```
$ infrata plan dev
Plan for project "demo", environment "dev":

No changes. Configuration matches the observed state.

Plan: 0 to create, 0 to update, 0 to replace, 0 to destroy, 0 to forget.
$ echo $?
0
```

The second `plan` is clean: everything `apply` created is now the observed state, and exits 0.

## The cloud file

A project with no `providers:` block writes its resources to `.infra/fake-cloud.json`, relative to
the project directory. A named instance (see [Several instances](#several-instances)) that does not
set `cloud:` gets its own file instead: `.infra/fake-cloud-<instance>.json`. Either way, `cloud:` in
`providers:` overrides the path — relative to the project, or absolute.

After the `apply` above, `.infra/fake-cloud.json` holds:

```json
{
  "resources": {
    "app-3": {
      "type": "fake.application",
      "address": "app",
      "attributes": {
        "database_url": "db-2.db.fake",
        "image": "nginx:1.27",
        "replicas": 1,
        "url": "https://app-3.fake"
      }
    },
    "db-2": {
      "type": "fake.database",
      "address": "db",
      "attributes": {
        "endpoint": "db-2.db.fake",
        "engine": "postgres",
        "network": "net-1",
        "password": "hunter2",
        "size": 10,
        "tags": {
          "team": "data"
        }
      }
    },
    "net-1": {
      "type": "fake.network",
      "address": "network",
      "attributes": {
        "cidr": "10.0.0.0/16",
        "id": "net-1"
      }
    }
  },
  "next_id": 3
}
```

| Key | Holds |
| --- | --- |
| `resources` | every resource, keyed by provider ID: its `type`, the infrata `address` that manages it (absent for resources infrata did not create), and its `attributes` as plain JSON |
| `failures` | injected failure rules — see below |
| `latency_ms` | simulated latency applied to every operation — see below |
| `next_id` | the counter used to allocate the next `net-N` / `db-N` / `app-N` ID |

**Editing this file by hand is supported, and is how you simulate drift.** Change `db-2`'s `engine`
from `"postgres"` to `"mysql"` and save, then plan again:

```
$ infrata plan dev
Plan for project "demo", environment "dev":

  ~ fake.application.app
      database_url: "db-2.db.fake" -> (known after apply)

  -/+ fake.database.db  (replacement forced by: engine)
    ⚠ This resource has 1 dependent resource.
      endpoint: "db-2.db.fake" -> (known after apply)
      engine: "mysql" -> "postgres"

Plan: 0 to create, 1 to update, 1 to replace, 0 to destroy, 0 to forget.
```

`engine` is `ForceNew` (see the tables below), so the hand-edit is reported as a forced replacement,
not a plain update — and the dependent `app` is reported too, because replacing `db` changes the
`database_url` it computes.

A resource you add by hand with no `address` — the way `net-77` might appear in this file if someone
provisioned it outside infrata — is infrastructure infrata did not create. That is what `infrata
discover` and `infrata import` are for. Add `net-77` to `.infra/fake-cloud.json` by hand:

```json
"net-77": {
  "type": "fake.network",
  "attributes": {"cidr": "172.16.0.0/12", "id": "net-77"}
}
```

```
$ infrata discover
TYPE              ID      NAME
fake.application  app-3   app-3
fake.database     db-2    db-2
fake.network      net-1   net-1
fake.network      net-77  net-77

4 resources found. Nothing has been imported.
Run `infrata import <environment> --generate` to adopt them.
$ echo $?
0
```

`discover` lists every resource the cloud file holds, managed or not — telling the two apart is
`import`'s job. `net-77` is the one this walkthrough added by hand, so it is the one to import:

```
$ infrata import dev fake.network.net-77 --generate
Wrote discovered/networks.yml
Imported net-77 as net-77

1 resource imported into "dev".
$ echo $?
0
```

`--generate` writes the configuration that keeps an imported resource from being destroyed on the
next apply:

```
$ cat discovered/networks.yml
# Generated by `infrata import`. Edit freely — this file is loaded like any
# other, and is the configuration that keeps imported resources from being
# destroyed on the next apply.
resources:
  # imported from net-77
  net-77:
    type: fake.network
    cidr: 172.16.0.0/12
```

```
$ infrata plan dev
Plan for project "demo", environment "dev":

No changes. Configuration matches the observed state.

Plan: 0 to create, 0 to update, 0 to replace, 0 to destroy, 0 to forget.
$ echo $?
0
```

The plan that follows is clean: `net-77` is now tracked under the configuration `import --generate`
wrote, with nothing left to create, update, or destroy.

## Resource types

### `fake.network`

No dependencies.

| Attribute | Kind | Flags |
| --- | --- | --- |
| `cidr` | string | Required, replaces on change |
| `id` | string | Computed |

Computed `id` is `net-N`, e.g. `net-1`.

### `fake.database`

Requires a `fake.network`.

| Attribute | Kind | Flags |
| --- | --- | --- |
| `engine` | string | Required, replaces on change |
| `size` | int | Default: `10` |
| `password` | string | Sensitive |
| `network` | string | |
| `tags` | map | |
| `endpoint` | string | Computed |

Computed `endpoint` is `db-N.db.fake`, e.g. `db-2.db.fake`.

### `fake.application`

Requires a `fake.database`.

| Attribute | Kind | Flags |
| --- | --- | --- |
| `image` | string | Required |
| `replicas` | int | Default: `1` |
| `database_url` | string | |
| `url` | string | Computed |

Computed `url` is `https://app-N.fake`, e.g. `https://app-3.fake`.

## Injecting failures and latency

Add a `failures` array to the cloud file:

```json
"failures": [
  {"op": "create", "address": "extra", "nth": 1, "retryability": "conditional", "message": "injected: quota exceeded"}
]
```

| Field | Meaning |
| --- | --- |
| `op` | `create`, `read`, `update`, `delete`, `discover`, or `import` |
| `address` | infrata's address for the resource; for `import` this is the provider ID being imported; for `discover` it is empty |
| `nth` | fires on the nth matching call, once (default `1`) |
| `retryability` | `not_safe` (the default when omitted), `conditional`, or `safe` |
| `message` | the error text infrata reports |

`retryability` decides what infrata's executor does next, and it does something different per
operation, not just per class:

| Operation | `not_safe` | `conditional` | `safe` |
| --- | --- | --- | --- |
| `create` | not retried | not retried | retried |
| `update` | not retried | retried | retried |
| `delete` | not retried | not retried | retried |
| `read`, `discover`, `import` | not retried | not retried | not retried |

`not_safe` is the right default whenever a second attempt might create a second resource or act on
whatever now has a deleted object's identity — which is why `create` and `delete` treat `conditional`
the same as `not_safe`: an ambiguous failure on either one might already have taken effect, and a
retry cannot tell. `update` treats `conditional` the same as `safe` instead, because making a
resource match the same desired state twice is harmless. `read`, `discover` and `import` are never
retried by the executor under any classification, so a `retryability` on one of those rules only
affects what `ClassifyError` reports — it changes nothing about whether infrata tries again. (When
"retried" applies: up to three attempts, waiting 500ms, then up to 10s, doubling and jittered between
attempts — infrata's policy, not this plugin's.) An unknown value (a typo, say `"nto_safe"`) is
refused with an error naming it, rather than silently treated as one of the three.

The plugin writes `seen` back onto the rule on every call that matches its `op` and `address`,
whether or not that call is the one that fires it, and sets `fired` on the call that does — so a
rule fires exactly once across however many times infrata reloads the cloud file. Delete both fields
to re-arm it.

Add simulated latency the same way:

```json
"latency_ms": 250
```

It delays every operation this instance performs, by that many milliseconds, applied concurrently
across whatever infrata has running at once — it does not serialize operations against each other.
If infrata cancels the context before the delayed operation starts, the delay is abandoned rather
than run to completion; once the operation itself has started, cancellation does not abandon it.

## Several instances

`providers:` can configure more than one instance of this plugin, each with its own cloud file —
`cloud:` stands in for a cloud account:

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

This is `e2e/testdata/instances/infra.yml`. `shared`, on the default instance `main`, is written to
`.infra/fake-cloud-main.json`; `isolated`, which names `provider: acct2`, is written to
`.infra/fake-cloud-acct2.json`. Each instance's `cloud:` key, if set, overrides its own file
independently — one instance never sees another's resources.

## Upgrading from infrata's built-in `test` provider

infrata used to carry this fake provider in-tree, serving `test.network`, `test.database`, and
`test.application`. As a plugin, it is renamed to `fake.network`, `fake.database`, and
`fake.application` — a plugin's types must be prefixed with the plugin's own name, and this plugin
is named `fake`, not `test`.

The implicit (unnamed) instance's cloud file keeps the same path it always had,
`.infra/fake-cloud.json`, so an existing cloud file needs no migration. infrata no longer ships a
built-in provider, and it migrates the state file for you: when it reads a version-1 state file it
rewrites every `test.*` type to `fake.*`, and renames an instance recorded as `test` (the name the
implicit instance took when a project declared no `providers:` block) to `fake`. An instance with
any other name, such as `providers: [{plugin: test, name: main}]`, keeps it; change that entry's
`plugin:` to `fake`.

## Development

```bash
go test -count=1 ./...
go vet ./...
gofmt -l .
```

Every test run uses `-count=1`: Go caches test results, and a cached pass would hide a fixture edit.

The suite above never touches infrata. The compliance suite does — it builds a real `infrata` from
source and this plugin, and runs the workflow above through both binaries for real:

```bash
go test -tags e2e -count=1 ./e2e/
```

It builds infrata from `$INFRATA_SRC` (default `../infrata`, the same sibling checkout `go.mod`'s
`replace` assumes), and skips itself with an `E2E SKIPPED:` line, rather than failing, when that
checkout is not present. Run it before every release.

## Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every push to `main` and every pull
request, and release.yml calls it. It has two jobs:

- **tag** (gating): does this plugin work with the infrata it declares? It drops `go.mod`'s `replace`,
  builds against the tagged infrata module `go.mod` requires, and runs gofmt, vet, the unit suite and
  the compliance suite against an infrata host built from that same tag. `scripts/ci-use-infrata-tag`
  does the module setup, for both workflows.
- **main** (advisory, never fails the run): has infrata `main` broken us? It builds through the
  `replace` against a fresh checkout of infrata's `main` and runs both suites.

Both need the `INFRATA_CHECKOUT_TOKEN` secret while infrata is private.

That tag build needs infrata's hashes in `go.sum`, and `go mod tidy` run locally with the `replace`
present strips them. The unit suite catches that (`TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds`)
and names the fix, which also restores them after you bump the infrata `require`:

```bash
scripts/ci-use-infrata-tag sum
```

## Releasing

`plugin.yaml`, at the repository root, is infrata's plugin manifest (`PLAN.md` §31.2) — what this
plugin is called, which protocol versions and platforms it supports, and where to find it. As
committed today:

```yaml
# plugin.yaml: what this plugin is, and what it works with. infrata PLAN.md §31.2.
# Read at a release TAG, never at the default branch, which describes unreleased code.
manifest: 1
name: fake
version: 0.2.0
# The protocol THIS RELEASE'S binary speaks: for an SDK-built plugin, exactly one version, the
# pluginproto.Version of the infrata go.mod requires. It changes in the same commit as that require
# (internal/fake/manifest_test.go and scripts/release-check refuse a mismatch), never goes stale,
# and a later host protocol bump forces no re-release: the host keeps accepting older versions.
protocol: [2]
platforms: [linux/amd64, linux/arm64, linux/arm, linux/386, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64]
description: A fake provider for testing infrata without a cloud account.
# The oldest infrata release CI verifies this plugin against: go.mod's require, which ci.yml
# builds and runs the e2e suite with. Nothing refuses a mismatched host at runtime yet; infrata
# checks this at install (PLAN.md §31.3), which is designed but not built.
infrata: ">= 0.3.0"
source: https://github.com/infrata/infrata-provider-fake
```

`infrata: ">= 0.3.0"` is the infrata release CI tests this plugin against. Today it is documentation
and an input to the compliance suite, not a runtime check: infrata will refuse a plugin whose floor
the running build fails at `infrata plugins install`, which is not built yet.

`protocol: [2]` is the plugin protocol this release's binary speaks. A binary built with infrata's
SDK speaks exactly one: the `pluginproto.Version` of the infrata `go.mod` requires, which v0.3.0
raised to 2. So `protocol:` changes in the same commit as that `require`, and both
`internal/fake/manifest_test.go` and `scripts/release-check` refuse a mismatch. Once released, the
value never goes stale — v0.1.1's binary announces protocol 1 for ever, and infrata v0.3.0 still
accepts 1 — so a later infrata protocol bump does not force a re-release.

infrata reads this file at a release tag, never at the tip of the default branch — the default
branch's `plugin.yaml` describes code that has not shipped yet. `internal/fake.Version` is
`0.0.0-dev` in any build that a release did not stamp, which is every build you make yourself with
plain `go build`; only a tagged release, built through the steps below via `-ldflags -X`, reports a
real version number.

Pushing a tag matching `v*` runs [`.github/workflows/release.yml`](.github/workflows/release.yml),
which:

1. Runs [CI](#continuous-integration) in full. Only its tag job gates the release: the tests run
   against the infrata release `go.mod` requires, not a working tree.
2. Switches to that same tagged infrata module (`scripts/ci-use-infrata-tag use`), then runs
   `scripts/release-check <tag>`, which refuses to continue if the git tag, `plugin.yaml`'s
   `version:`, and the version the built binary's handshake reports disagree, or if `plugin.yaml`'s
   `protocol:` is not exactly the protocol version that handshake announces.
3. Runs `scripts/build-release <version> dist`, which cross-compiles every platform `plugin.yaml`
   lists into `dist/infrata-plugin-fake_<version>_<goos>_<goarch>.tar.gz` (`.zip` for `windows`).
4. Writes `dist/SHA256SUMS` and publishes a GitHub release with all of it attached.

`scripts/release-check` is also how you check locally, before ever pushing a tag, that a release
will not be refused:

```
$ scripts/release-check v0.2.0
release-check: tag, plugin.yaml and binary all say 0.2.0, and speak protocol [2]
$ echo $?
0
```

It builds a throwaway binary into a temporary directory to check its version — nothing under this
repository is written or left behind.

## Writing your own provider

**[`AGENT.md`](AGENT.md)** is the condensed plugin-authoring reference — the API, the rules, and the
failure modes, written to be copied into your own plugin repository and used by a coding agent.
**[`docs/writing-a-provider.md`](docs/writing-a-provider.md)** is the long-form guide for a developer
who knows their cloud's API and nothing about infrata.

## Licence

TBD.
