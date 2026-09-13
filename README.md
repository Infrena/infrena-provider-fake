# infrata-provider-fake

The fake provider for [infrata](https://github.com/infrata/infrata), distributed as a plugin
binary: `infrata-plugin-fake`. Its "cloud" is a hand-editable JSON file on disk, so infrata's whole
engine — planning, applying, drift detection, import, and failure handling — can be exercised with
no network and no credentials.

## Build and install

This plugin builds against a sibling checkout of infrata, because `github.com/infrata/infrata` is
not yet published as a fetchable module. Lay the two repositories out side by side:

```
some-directory/
├── ilan/                    # infrata itself
└── infrata-provider-fake/   # this repository
```

`go.mod`'s `replace github.com/infrata/infrata => ../ilan` assumes exactly that layout. Then:

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
discover` and `infrata import` are for.

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

`retryability` decides what infrata's executor does next: `not_safe` never retries — the right
default whenever a second attempt might create a second resource; `conditional` is retried, but
cautiously, because the operation might already have taken effect; `safe` is retried freely, because
the operation provably did not take effect. An unknown value (a typo, say `"nto_safe"`) is refused
with an error naming it, rather than silently treated as one of the three.

The plugin writes `seen` and `fired` back onto the rule as it fires, so a rule fires exactly once
across however many times infrata reloads the cloud file; delete those two fields to re-arm it.

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
`.infra/fake-cloud.json`, so an existing cloud file needs no migration. A state file that names
`test.*` resources is not migrated by this plugin, and those resources keep working only for as
long as infrata still ships its builtin `test` provider alongside this one; moving a project onto
`infrata-plugin-fake` means re-creating its resources under the `fake.*` type names.

## Development

```bash
go test -count=1 ./...
go vet ./...
gofmt -l .
```

Every test run uses `-count=1`: Go caches test results, and a cached pass would hide a fixture edit.

## Writing your own provider

**[`AGENT.md`](AGENT.md)** is the condensed plugin-authoring reference — the API, the rules, and the
failure modes, written to be copied into your own plugin repository and used by a coding agent.
**[`docs/writing-a-provider.md`](docs/writing-a-provider.md)** is the long-form guide for a developer
who knows their cloud's API and nothing about infrata.

## Licence

TBD.
