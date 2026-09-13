module github.com/infrata/infrata-provider-fake

go 1.27.0

require github.com/infrata/infrata v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// infrata stays private until it is feature complete (infrata PLAN.md §31.1), so this plugin builds
// against a sibling checkout at ../infrata — the directory `git clone git@github.com:infrata/infrata.git`
// creates. Replace this with a real version above once infrata is published.
replace github.com/infrata/infrata => ../infrata
