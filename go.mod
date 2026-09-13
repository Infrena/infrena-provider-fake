module github.com/infrata/infrata-provider-fake

go 1.27.0

require github.com/infrata/infrata v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// infrata is not yet published as a fetchable module, so this plugin builds against a
// sibling checkout at ../ilan. An outside plugin author has the same constraint today.
// Delete this line, and pin a real version above, once infrata is published.
replace github.com/infrata/infrata => ../ilan
