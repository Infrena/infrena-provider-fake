// Command infrata-plugin-fake is infrata's fake provider. Infrata runs it; you do not.
package main

import (
	"github.com/infrata/infrata-provider-fake/internal/fake"
	"github.com/infrata/infrata/pkg/pluginsdk"
)

func main() { pluginsdk.Main(fake.NewPlugin()) }
