# Publishing Packages

There is no registry. agentpack resolves packages straight from GitHub, using Git tags as versions. Publishing a package is just pushing a repo and tagging it.

A package is one of two kinds, detected by its layout:

- **Skill** — a directory containing a `SKILL.md`.
- **Plugin** — a directory containing a plugin manifest: `.claude-plugin/plugin.json`, `.cursor-plugin/plugin.json`, or both.

You do **not** declare your artifacts in a table. agentpack discovers them by reading the package's content and re-renders them per harness (see [Cross-Harness Conversion](../harnesses/conversion.md)).

## A skill package

The minimal skill is a directory with a `SKILL.md`:

```text
canvas-design/
  SKILL.md
  reference.md      # any supporting files the skill references
```

Consumers depend on the directory's module ID:

```toml
[dependencies]
"github.com/acme/skills/canvas-design" = "^1.0"
```

## A plugin package

A plugin carries a manifest plus whatever artifact directories it ships. Layouts are normalized after fetch, so you can expose Claude and/or Cursor manifests:

```text
hookify/
  .claude-plugin/plugin.json
  commands/   agents/   skills/   rules/   hooks/
  mcp.json
```

## Transitive dependencies

If your package depends on other packages, add an `agentpack.toml` to its root declaring `[dependencies]`. When someone depends on your package, agentpack reads that manifest at the resolved commit and pulls your dependencies into their graph. The manifest's `[dependencies]` table is the only part consulted for transitive resolution.

## Versioning and tags

agentpack resolves SemVer tags. Both `v1.0.0` and `1.0.0` are recognized:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Suggested bump policy:

| Change | Bump |
|---|---|
| New artifact, backward compatible | MINOR |
| Content-only fix | PATCH |
| Artifact removed/renamed, or format break | MAJOR |

## Monorepo packages

Put the package (its `SKILL.md` or plugin manifest, and an `agentpack.toml` if it has dependencies) in a subdirectory, and tag the repo normally. Consumers reference the full subpath in the module ID:

```toml
[dependencies]
"github.com/acme/monorepo/packages/my-skills" = "^1.0"
```

agentpack reads from that subdirectory at the tagged commit.

## Test before you tag

Develop against a consumer project using a local path dependency, then switch to a version constraint once you're happy:

```toml
[dependencies]
"my-skills" = { path = "../my-skills" }
```

Run `agentpack sync` and inspect the staged output (the [harness guides](../harnesses/claude.md) show where each tree lives).

## Private repositories and rate limits

agentpack reads `GITHUB_TOKEN` (or `GH_TOKEN`) when present and sends it as a bearer token for ref/tag lookups and authenticated downloads. Set one to access private repos, and to avoid anonymous API rate limits on heavy resolution:

```sh
export GITHUB_TOKEN="ghp_..."
```
