module github.com/infrata/infrata-provider-fake

go 1.27.0

require github.com/infrata/infrata v0.2.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// The require above is the infrata RELEASE this plugin is built and tested against, and the one
// CI verifies: scripts/ci-use-infrata-tag drops the replace below, so CI compiles the tagged
// module fetched from github.com/infrata/infrata, checked against the hashes committed in go.sum.
//
// The replace is for local work only. infrata stays private until it is feature complete (infrata
// PLAN.md §31.1), so a local build uses the sibling checkout at ../infrata — the directory
// `git clone git@github.com:infrata/infrata.git` creates — whatever is on disk there.
//
// go.sum must carry infrata's hashes for the required version, which `go mod tidy` strips while
// this replace is present. After bumping the require, or after a tidy, restore them with:
//
//	scripts/ci-use-infrata-tag sum
replace github.com/infrata/infrata => ../infrata
