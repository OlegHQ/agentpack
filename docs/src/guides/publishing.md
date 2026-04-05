# Publishing Packages

Any GitHub repository can be an agentpack package. There is no registry — agentpack resolves packages directly from GitHub using tags as versions. Publishing a package means tagging a release.

## Package structure

At minimum, a publishable package needs an `agentpack.toml` with `[package]` metadata at the root (or subdirectory) that consumers will reference:

```toml
# agentpack.toml in your package repo
[package]
name    = "my-agent-skills"
version = "1.0.0"
```

Beyond the manifest, the package contains the actual artifact files. Typical layout:

```
my-package/
  agentpack.toml
  commands/
    review.md
    explain.md
  agents/
    code-reviewer.md
  rules/
    style-guide.md
```

## Artifact declarations

Declare your artifacts in `agentpack.toml` so agentpack knows what to stage and how:

```toml
[package]
name    = "my-skills"
version = "1.0.0"

[[artifact]]
type = "command"
name = "review"
file = "commands/review.md"

[[artifact]]
type = "agent"
name = "code-reviewer"
file = "agents/code-reviewer.md"

[[artifact]]
type = "rule"
name = "style-guide"
file = "rules/style-guide.md"
```

## Versioning and tagging

agentpack resolves versions from Git tags. Use SemVer tags:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Both `v1.0.0` and `1.0.0` formats are recognized.

### When to bump versions

| Change type | Version bump |
|---|---|
| New artifact added (backward compatible) | MINOR |
| Artifact removed or renamed | MAJOR |
| Content-only fix (no structural change) | PATCH |
| Breaking change to artifact format | MAJOR |

## Monorepo packages

If your package lives in a subdirectory of a larger repo, place `agentpack.toml` in that subdirectory. Consumers reference it with the full subpath:

```toml
# Consumer's agentpack.toml
[dependencies]
"github.com/acme/monorepo/packages/my-skills" = "^1.0"
```

Tag the repo with SemVer tags as normal. agentpack reads the manifest from the subdirectory at the tagged commit.

## Testing your package locally

Before publishing, test your package as a local dependency:

```toml
# In your test project's agentpack.toml
[dependencies]
"github.com/acme/my-skills" = { path = "../my-skills" }
```

Run `agentpack sync` to stage and inspect the output. Then switch to a version constraint once you're satisfied and publish the tag.

## Visibility

agentpack works with public GitHub repositories. Private repository support depends on having a valid `GITHUB_TOKEN` with read access to the private repo in your environment.
