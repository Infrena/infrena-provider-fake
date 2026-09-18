module github.com/infrena/infrena-provider-fake

go 1.27.0

require github.com/infrena/infrena v0.11.1

require gopkg.in/yaml.v3 v3.0.1 // indirect

// The require is the infrena RELEASE this plugin is built and tested against: v0.11.1. Every
// package this plugin compiles against — pkg/pluginproto, pkg/pluginsdk, pkg/plugintest,
// pkg/provider, pkg/schema, pkg/value, pkg/resource, pkg/address, pkg/pluginmanifest, pkg/semver —
// is byte for byte what v0.7.0 shipped. v0.8.0 through v0.11.1 added the state backend plugin
// interface (pkg/backend, pkg/backendproto, pkg/backendsdk, pkg/backendtest), which a provider does
// not use, and a report line for `state migrate --check` (pkg/report, Version 2 to 3), which this
// repository does not import. So this bump is a pin move, not an API move.
//
// pluginproto.Version is still 4, which is what this binary speaks (a discovered resource can carry
// SystemOwned, which this plugin never sets: its cloud creates nothing for itself). plugin.yaml's
// `protocol: [4]` therefore does NOT move with this require.
//
// Nor does plugin.yaml's `infrena: ">= 0.7.0"` floor, and the difference is the point: the floor is
// the OLDEST host that accepts this binary, which is v0.7.0 — the release that raised the protocol
// to 4, so a v0.6.x host refuses the handshake. The require is the NEWEST host it is tested with.
// v0.6.0 added the References fake.database's network declares, and protocol 3; v0.6.1 made the
// host adapter check references on every load. v0.5.0 changed the configuration grammar to write a
// variable as ${var.x}, which every example in this repository uses. v0.4.0 is the oldest release a
// require on this path can name: the project was renamed Infrena on 2026-09-14, and tags v0.1.0 to
// v0.3.0 declare its former module path.
//
// THERE IS NO `replace`, on purpose. Until 2026-09-17 this file carried
// `replace github.com/infrena/infrena => ../infrena` and scripts/ci-use-infrena-tag stripped it in
// CI. That is the wrong way round: a committed replace has to be REMOVED to be correct, so the
// correctness of every release depended on a script running. Local work against a sibling checkout
// now goes through a gitignored go.work (`go work init . ../infrena`), which is invisible to the
// module graph and absent from a fresh clone — so the DEFAULT build is the pinned, correct one, and
// building against a working tree is the deliberate exception. CI and releases set GOWORK=off.
//
// infrena stays private until it is feature complete (infrena PLAN.md §31.1), so fetching it needs
// GOPRIVATE=github.com/infrena/* and git credentials for github.com/infrena — `gh auth setup-git`
// locally, the INFRENA_CHECKOUT_TOKEN secret in CI. `go mod tidy` and `go get` ignore go.work, so
// they always need them, workspace or not.
