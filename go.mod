module github.com/infrena/infrena-provider-fake

go 1.27.0

require github.com/infrena/infrena v0.14.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// The require names the infrena RELEASE this plugin is built and tested against. It is not the
// same thing as plugin.yaml's floor, and the difference is the point: the floor is the OLDEST
// host that accepts this binary, which is the release that raised the protocol the binary
// speaks, because an older host refuses it at the handshake. The require is the NEWEST host it
// is tested with. Raising the floor to match the require would refuse hosts this binary works
// perfectly well with.
//
// A require bump moves plugin.yaml's `protocol:` only when it moves pluginproto.Version. Most do
// not. internal/fake/manifest_test.go and scripts/release-check both refuse a manifest whose
// protocol is not exactly what the built binary announces, so the two cannot drift apart
// unnoticed.
//
// THERE IS NO `replace`, on purpose. Until 2026-09-17 this file carried
// `replace github.com/infrena/infrena => ../infrena` and scripts/ci-use-infrena-tag stripped it in
// CI. That is the wrong way round: a committed replace has to be REMOVED to be correct, so the
// correctness of every release depended on a script running. Local work against a sibling checkout
// now goes through a gitignored go.work (`go work init . ../infrena`), which is invisible to the
// module graph and absent from a fresh clone — so the DEFAULT build is the pinned, correct one, and
// building against a working tree is the deliberate exception. CI and releases set GOWORK=off.
//
// `go mod tidy` and `go get` ignore go.work, so they always resolve infrena from the module graph
// rather than from a sibling checkout, workspace or not.
