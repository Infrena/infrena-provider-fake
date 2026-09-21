// Command infrena-plugin-fake is infrena's fake provider. Infrena runs it; you do not.
package main

import (
	"github.com/infrena/infrena-provider-fake/internal/fake"
	"github.com/infrena/infrena/pkg/pluginsdk"
)

func main() { pluginsdk.Main(fake.NewPlugin()) }
