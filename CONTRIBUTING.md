# Contributing to infrena-provider-fake

Thanks for wanting to help. This plugin has two jobs, and a change is worth judging against
both:

1. **It is how infrena's engine is tested.** Planning, applying, drift, import and failure
   handling are all exercised against this provider, with no network and no credentials.
2. **It is the worked example for writing a provider.** Someone building their own plugin
   reads this repository to see what the shape is.

A change that makes the first job easier while making the second harder to read is usually
the wrong trade.

## Before you start

**Open an issue first for anything non-trivial.** In particular, adding a resource type or
changing how the fake cloud behaves affects the engine's own test suite, which lives in a
different repository.

Small fixes — a typo, a wrong comment, a genuinely broken thing — just send them.

If the problem is in the engine rather than this plugin, it belongs in
[infrena/infrena](https://github.com/infrena/infrena/issues).

## What this repository expects of code

**The fake cloud is a file a human can edit.** That is the point: a test induces drift by
editing JSON on disk, and a person debugging can read it. Anything that makes that file
harder to read or edit by hand costs more than it saves.

**Be honest about failure.** This provider is how the engine's error paths get exercised, so
the ways it can fail are a feature. If you add a failure mode, make it deliberate and
documented rather than incidental.

**Stay boring.** This is the reference a plugin author copies. Clever is worse than obvious
here, even where clever is shorter.

## Running the tests

```bash
# The ordinary suite. No network, no credentials.
go test ./...

# End to end, driving a real infrena binary against this plugin.
go test -tags e2e -count=1 ./e2e/
```

The end-to-end suite needs an infrena checkout to build the engine from. By default it looks
for one beside this repository; `INFRENA_SRC` points it somewhere else. It **skips** when it
cannot find one, and says so — CI treats that skip as a failure, because a run that silently
skips the only tests exercising the real engine is a green tick that means nothing.

CI builds two ways: against the engine version `go.mod` requires (`GOWORK=off`), and against
the engine's current `main`. The first gates a merge; the second is early warning that a
change in the engine is coming.

To develop against a local engine checkout, create a `go.work` — it is deliberately not
committed, so a fresh clone builds against the published engine rather than whatever happens
to be next door.

## Commit messages

Explain why, not what. The diff already says what changed.

## Why there is a CLA

Contributions are accepted under a [Contributor License Agreement](CLA.md). The reasoning is
the same as for the rest of Infrena and is set out in full there: the project is open core,
and the CLA is what lets contributed code ship in both halves without anyone's contribution
being relicensed out from under them.

Sign it once and it covers your contributions to every Infrena repository.

## Reporting problems

- **A security vulnerability:** [SECURITY.md](SECURITY.md). Do not open a public issue.
- **Anything else:** an issue here, or in the engine repository if that is where it lives.

By contributing you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).
