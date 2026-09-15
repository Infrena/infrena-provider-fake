module github.com/infrena/infrena-provider-fake

go 1.27.0

require github.com/infrena/infrena v0.6.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// The require is the infrena RELEASE this plugin is built and tested against: v0.6.0, the release
// that added provider-declared references (schema.Attribute.References, which fake.database's
// network declares) and raised the plugin protocol to 3, which is therefore what this binary
// speaks. v0.5.0 before it changed the configuration grammar to write a variable as ${var.x}, which
// every example in this repository uses. v0.4.0 is the oldest release a require on this path can
// name: Infrata was renamed Infrena on 2026-09-14, and tags v0.1.0 to v0.3.0 declare the old path,
// github.com/infrata/infrata.
//
// It is also the release CI verifies: scripts/ci-use-infrena-tag drops the replace below, so CI
// compiles the tagged module fetched from github.com/infrena/infrena, checked against the hashes
// committed in go.sum.
//
// The replace is for local work. infrena stays private until it is feature complete (infrena
// PLAN.md §31.1), so a local build uses the sibling checkout at ../infrena — the directory
// `git clone git@github.com:Infrena/infrena.git` creates — whatever is on disk there.
//
// go.sum must carry infrena's hashes for the required version, which `go mod tidy` strips while
// this replace is present. After bumping the require, or after a tidy, restore them with:
//
//	scripts/ci-use-infrena-tag sum
replace github.com/infrena/infrena => ../infrena
