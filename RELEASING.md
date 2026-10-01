# Releasing

How a vellum version gets from `main` to `ghcr.io/freema/vellum`. This is a
maintainer document. Contributors only need the *Unreleased* section of the
changelog (see [CONTRIBUTING.md](CONTRIBUTING.md)).

## What a tag does

Pushing a tag `vX.Y.Z` runs [`.github/workflows/release.yml`](.github/workflows/release.yml):

1. builds the image for `linux/amd64` and `linux/arm64` with the version
   baked in (`-ldflags -X main.version`, shown by `vellum -version` and
   `/healthz`),
2. pushes it to ghcr as `X.Y.Z`, `X.Y` and `latest`,
3. creates the GitHub Release with the notes taken from the matching
   `## [X.Y.Z]` section of `CHANGELOG.md`.

A tag with a hyphen (`v1.13.0-beta.1`) is a **pre-release**. It gets only
its own image tag, never moves `latest` or `X.Y`, is marked as a
pre-release on GitHub, and takes its notes from the section of the version
it is a candidate for (`## [1.13.0]`).

## A normal release

1. Make sure `main` is green and the *Unreleased* section says everything a
   user upgrading needs to know.
2. Rename `## [Unreleased]` to `## [X.Y.Z] — YYYY-MM-DD`, add a fresh empty
   `## [Unreleased]` above it, and commit on `main`:

   ```sh
   git commit -am "release: X.Y.Z — <one line on what matters most>"
   ```

3. Tag and push:

   ```sh
   git tag -a vX.Y.Z -m "release: X.Y.Z — <same line>"
   git push origin main vX.Y.Z
   ```

4. Watch the *Release* workflow, then check the result:

   ```sh
   docker run --rm ghcr.io/freema/vellum:X.Y.Z -version
   gh release view vX.Y.Z
   ```

Version numbers follow [SemVer](https://semver.org/): a new tool, endpoint
or setting is a minor release; a fix is a patch release. Changing a default
or removing something is announced in the changelog with what to do about
it.

## A security fix for the current stable line

When `main` already carries unreleased features (a beta), do not ship them
with the fix. Release the fix as a patch of the latest stable minor from a
release branch, and as the next pre-release from `main`:

```sh
# 1. fix on main through a PR, as usual (merged as <sha>)

# 2. patch release of the stable line
git switch -c release/X.Y vX.Y.Z          # the latest stable tag
git cherry-pick -x <sha>
# CHANGELOG: a ## [X.Y.Z+1] section, without notes about unreleased features
git commit -am "release: X.Y.Z+1 — <the fix>"
git tag -a vX.Y.Z+1 -m "release: X.Y.Z+1 — <the fix>"
git push origin release/X.Y vX.Y.Z+1

# 3. next pre-release from main
git switch main
git tag -a vX.Y+1.0-beta.N -m "…"
git push origin vX.Y+1.0-beta.N

# 4. record the patch release in main's history, then drop the branch
git switch -c chore/merge-back-release-X.Y main
git merge -s ours --no-ff release/X.Y   # main already has the fix
# PR, merged with "Create a merge commit" (a squash would lose the link)
git push origin --delete release/X.Y    # the tag keeps the commits
```

The workflow file that runs is the one **in the tagged commit**, so a
release branch cut from an older tag uses that tag's `release.yml`.

Step 4 matters for how GitHub presents the release: without it the
release branch shows as commits ahead of `main`, and the patch tag is not
reachable from `main`, although the fix is there. Copy the patch's
changelog section into `main`'s `CHANGELOG.md` in the same PR. A later
patch of the same line starts again from its latest tag:
`git switch -c release/X.Y vX.Y.Z`.

### The advisory

Prepare the fix in private when a reporter is involved:

1. Draft the advisory first (repo → *Security* → *Advisories* → *New draft*,
   or `gh api -X POST repos/freema/vellum/security-advisories`). Affected
   package: ecosystem `go`, name `github.com/freema/vellum`. List one range
   per affected line (`>= A, < X.Y.Z+1` and `= X.Y+1.0-beta.N-1`).
2. Use its GHSA ID in the changelog entry and the PR.
3. Release as above.
4. Verify the published image, not only the code: run it and repeat the
   reproduction against it.
5. Publish the advisory. Request a CVE from the draft *before* publishing
   if one is wanted, because GitHub assigns it during review.

## Things that do not update themselves

- `server.json` and `.claude-plugin/marketplace.json` carry a version
  number. Bump them when the MCP registry entry or the plugin listing
  should point at the new release.
- `SECURITY.md` → *Supported versions*, when a new minor becomes the stable
  line.
