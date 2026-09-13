# infrata-provider-fake

The fake provider for [infrata](https://github.com/infrata/infrata), distributed as a plugin
binary: `infrata-plugin-fake`.

It manages nothing real. Its "cloud" is a hand-editable JSON file on disk, which is exactly the
point — it makes infrata's whole engine testable without cloud credentials, and it lets you
simulate drift by opening a file in an editor.

> **Status: not yet built.** This repository currently holds planning and authoring documentation.
> `CLAUDE.md` describes the work; this README is replaced by a real one as part of it.

## Why this is a separate repository

It has two jobs. The first is to replace the fake provider that currently lives inside infrata's
own module. The second is to be the worked example every plugin author reads — it is the only
plugin whose source anyone can study.

The second job is why it moved out of infrata's tree. A plugin living inside the engine's module
can quietly depend on something an external author cannot have, and nobody would find out until the
first third-party plugin failed. Here, if it compiles, the dependency is one an outside author has
too.

## Writing your own provider

Start with **[`AGENT.md`](AGENT.md)**. It is the plugin authoring guide — the API, the rules, and
the failure modes — written to be used by a coding agent and portable to any plugin. Copy it into
your own repository.

The specification it implements is `PLAN.md` §31.1 in the infrata repository.

## What a plugin is, in one screen

An ordinary Go program. Infrata launches it as a child process and talks to it over stdin/stdout in
newline-delimited JSON; you never write any of that.

```go
package main

import (
	"github.com/infrata/infrata/pkg/pluginsdk"
	"github.com/example/infrata-plugin-hetzner/internal/hetzner"
)

func main() { pluginsdk.Main(hetzner.New()) }
```

You implement two interfaces — `provider.Plugin` (name, schemas, and how to configure one instance
of yourself) and `provider.Provider` (read, create, update, delete, discover, import, and error
classification). Everything else is the SDK's.

## Licence

TBD.
