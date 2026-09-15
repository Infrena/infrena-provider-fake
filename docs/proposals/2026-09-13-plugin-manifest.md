# Proposal: a well-known plugin manifest, `plugin.yaml`

**Status: ACCEPTED WITH AMENDMENTS, 2026-09-13.** The agreement is **infrena `PLAN.md` §31.2**,
which is what this repository builds against — not the schema below, which differs from it in five
places. Read §31.2 first; this file is kept for the reasoning and the facts it established.

**From:** the `infrena-provider-fake` port. Every fact and line number cited below was verified
against infrena `fba0751` during the review and checked out.

**What §31.2 changed, and why:**

1. **Read the manifest at the git TAG, never the default branch.** The file at the repo root on
   `main` describes UNRELEASED code, so judging `v0.3.1` by it answers the wrong question — and
   `raw.githubusercontent.com/<owner>/<repo>/HEAD/plugin.yaml` is the obvious URL an implementer
   reaches for. Use `refs/tags/<tag>/plugin.yaml`.
2. **`manifest: 1` added**, checked before any other key. The configuration language deliberately
   is NOT versioned (§61.2); this is the other case, because a manifest is read over the network by
   every infrena build for years with no way to upgrade the reader in step with the writer.
3. **`platforms` added** (`GOOS/GOARCH` per published build), so "is there a build for my machine"
   is answerable from the file rather than by listing release assets.
4. **`description` required.** The purpose is SEARCH, and the proposed file said nothing about what
   a plugin is.
5. **`infrena` made OPTIONAL**, which answers this proposal's own open question: `">= 0.0.0"` is a
   value shaped like a constraint that constrains nothing, and absence says the same thing honestly.

**Asks, answered:** (1) schema agreed as amended. (2) DONE — `internal/semver` is now
`pkg/semver`, importable from this module. (3) read it at INSTALL (Phase B); a plugin with no
manifest installs with a warning rather than being refused, since Phase A is hand-placed binaries.
The handshake route is deferred, not rejected. (4) yes.

**Recorded as deliberately absent from the manifest:** checksums (they postdate the build;
`SHA256SUMS` is a release asset and Phase B's `plugins.lock` records them), asset names (a
convention mirroring infrena's own releases: `infrena-plugin-<name>_<version>_<goos>_<goarch>.tar.gz`),
and resource types (`name` already implies them).

**One reversal to note:** the review initially suggested embedding the manifest so the code derives
from it, then withdrew that. The manifest is authoritative and the binary secondary, so what matters
is a release-time assertion that the tag, the manifest's `version` and the binary's `Version()` all
agree — which blocks a release, where a drift test is something a person can delete.

---

*Original proposal follows.*

## Why

A plugin repository should say, in one file a person or a tool can read without building
anything, which plugin it is, which version it is, which plugin protocol versions it speaks,
and which infrena releases it works with.

`infrena version` already answers the host's half of that question:

```text
infrena 0.0.0-dev (fba0751, go1.27.0, linux/amd64)

formats
  state            1
  plugin protocol  1
  plan artifact    1
  report           1
```

`plugin.yaml` is the plugin's half.

## The file

At the root of a plugin repository, next to `go.mod`:

```yaml
# plugin.yaml: what this plugin is, and what it works with.
name: fake
version: 0.1.0
protocol: [1]
infrena: ">= 0.1.0"
```

| Key | Required | Meaning | Must agree with |
| --- | --- | --- | --- |
| `name` | yes | the plugin's name | the binary `infrena-plugin-<name>`, `Plugin.Name()`, and every resource type's prefix. The host already refuses a mismatch between the last two. |
| `version` | yes | `MAJOR.MINOR.PATCH` | `Version()`, which the handshake reports and a project's `plugins:` constraint is checked against (`internal/pluginhost/loader.go:139`) |
| `protocol` | yes | every plugin protocol version the plugin can speak: a non-empty list | the SDK the plugin is built with. Today `pluginsdk` speaks exactly `pluginproto.Version` (1), so every SDK-built plugin writes `[1]`. It is a list because the host accepts a set (`pluginproto.Supported`) and `infrena version` prints one. |
| `infrena` | yes | the infrena releases the plugin is known to work with | infrena's own constraint syntax (`internal/semver`): `>=` `<=` `!=` `==` `>` `<` `=`; a comma means AND; a bare version pins exactly; `0.4` means `0.4.0`; a pre-release suffix is ignored |

Unknown keys are refused, the same fail-closed rule a plugin applies to its configuration.

### What "compatible" means

A plugin and an infrena build are compatible when both of these hold:

1. **Protocol:** `protocol` shares at least one version with the build's `plugin protocol` set.
2. **Release:** `infrena` allows the build's version. A development build (`0.0.0-dev`, `0.0.0`,
   or an unparseable version) is **exempt**, the same exemption `checkRequiredVersion` gives a
   project's `infrena:` floor (`internal/compiler/compile.go:252-265`) and for the same reason:
   a complaint about a developer's own build is not something they can act on.

Rule 1 is already enforced at runtime by the handshake. Rule 2 is enforced nowhere today,
because nothing reads the manifest.

## What a plugin repository can do alone (no infrena change)

- Ship `plugin.yaml`.
- **Unit test** (normal suite): `name == PluginName`, `version == Version`,
  `protocol == [pluginproto.Version]`. This keeps the file from drifting from the code.
- **Compliance test** (behind a build tag): run `infrena version --output <file>` for the infrena
  under test, and assert that `formats[name="plugin protocol"].versions` intersects `protocol`.
- **What it cannot do:** check the `infrena` constraint.
  - The parser is `internal/semver`, which another module cannot import, so a plugin would have
    to reimplement it (see ask 2).
  - An infrena built from a checkout reports `0.0.0-dev` and is exempt from rule 2, so the test
    exercises nothing unless it stamps a release version
    (`-ldflags "-X github.com/infrena/infrena/internal/version.version=0.1.0"`).

## Asks of infrena

1. **Agree the schema**, or amend it. This repository's file follows whatever is agreed.
2. **Publish the constraint parser** (`internal/semver` → `pkg/semver`), so a plugin can validate its
   own `infrena:` field without a second implementation that drifts from the first.
3. **Decide whether and where infrena reads the manifest.** The file lives in the repository, not
   next to the binary on the plugin search path, so reading it at runtime needs one of:
   - **a. At install (recommended).** Phase B's `infrena plugins install` reads `plugin.yaml` from
     the release, alongside the binary it checksums, and refuses a plugin that fails rule 1 or 2
     before installing it. No protocol change.
   - **b. In the handshake.** The plugin embeds the file (`//go:embed plugin.yaml`) and the SDK sends
     the `infrena` constraint as a new optional handshake field. That is an additive `pluginproto`
     change, and the host warns or refuses when the running release falls outside the constraint.
   - **c. Neither.** The manifest is documentation and CI only.
4. **Optional:** `--verbose` already prints where each plugin was loaded from. It could also print
   the plugin's version and protocol, which the host already knows from the handshake.

## Open question for the owner

What does a plugin developed against an unreleased infrena write in `infrena`? `">= 0.0.0"` admits
everything, which is true but says nothing. Naming the first release it will support is more
useful, but it cannot be tested until that release exists.

## Facts this proposal rests on

Checked on 2026-09-13 against infrena `fba0751`, built with go1.27.0.

| Claim | Where |
| --- | --- |
| `infrena version --output` writes `{"version", "revision", "go", "platform", "formats": [{"name", "versions"}]}` | run against the build above |
| `plugin protocol` is a set, read from `pluginproto.Supported` | `internal/cli/version.go:58` |
| a project `infrena:` floor is enforced for release builds; development builds are exempt | `internal/compiler/compile.go:252-270` |
| `plugins:` constraints are enforced against the handshake version; an unversioned plugin reports `0.0.0` and cannot satisfy one | `internal/pluginhost/loader.go:139-160` |
| the constraint syntax | `internal/semver/semver.go` |
| `internal/semver` cannot be imported from another module | Go's `internal/` rule |
| nothing in infrena reads a plugin manifest | no reference to `plugin.yaml` or a manifest in `internal/` or `pkg/` |
