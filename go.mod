module github.com/infrena/infrena-provider-fake

go 1.27.0

require github.com/infrena/infrena v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// v0.0.0 IS A PLACEHOLDER, NOT A RELEASE. Infrata was renamed Infrena on 2026-09-14, and its
// module path moved from github.com/infrata/infrata to github.com/infrena/infrena. No release has
// been tagged on the new path yet: tags v0.1.0 to v0.3.0 declare the old module path, so none of
// them can satisfy this require. v0.4.0 will be the first renamed release, and it cannot be tagged
// until this plugin has renamed (the engine's CI builds cmd/infrena-plugin-fake from a sibling
// checkout). Until then the replace below is the only way this module builds, and:
//
//   - scripts/ci-use-infrena-tag refuses v0.0.0 by name, so CI's gating tag job is red rather
//     than silently green;
//   - TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds skips, saying why, because there are no
//     hashes to pin for a version that does not exist.
//
// Step 3 of the rename bumps this require to v0.4.0 and runs `scripts/ci-use-infrena-tag sum`,
// which restores go.sum's infrena hashes and ends both of those.
//
// Once it names a release, the require is the infrena RELEASE this plugin is built and tested
// against, and the one CI verifies: scripts/ci-use-infrena-tag drops the replace below, so CI
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
