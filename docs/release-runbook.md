# Release runbook

Moji has one repository-owned release command. It rebuilds the six npm native
binaries, packs the exact npm archive, extracts it, and verifies every artifact
before an irreversible operation can run.

## Verify a release archive

Update and commit the version in `package.json` and `package-lock.json`, then run:

```console
npm run release
```

Verification is the default mode. It does not publish a package, create a tag,
push, or create a GitHub release. The command:

1. Builds Linux, macOS, and Windows binaries for x64 and arm64.
2. Runs `npm pack` with lifecycle scripts explicitly enabled, even when the
   user's npm configuration sets `ignore-scripts=true`.
3. Extracts the generated tarball into a temporary directory.
4. Confirms the packed manifest version and exact six-target directory set.
5. Confirms each binary's embedded app version and Go target metadata.
6. Runs the packed JavaScript launcher and compares its reported version.

CI runs this same command so stale or incorrectly targeted package artifacts
cannot pass the release gate.

## Publish

The publishing operator needs npm and GitHub CLI authentication. The repository
must be clean, and the matching `v<version>` tag must not exist.

```console
npm run release:publish
```

After verification passes, the command publishes the exact verified tarball,
creates and pushes an annotated `v<version>` tag, then creates the matching
latest GitHub release with generated notes and the tarball attached.

## Update the Homebrew formula

`Formula/moji.rb` is the repository-owned tap formula. It deliberately installs
the native binary from the same immutable npm archive verified by the release
command instead of introducing another binary build or upload path.

After `npm run release:publish` has published the new version, download that
archive, calculate its SHA-256 checksum, and update the formula's `url` and
`sha256`. Then verify the formula on Homebrew before committing the formula
update:

```console
VERSION=0.8.0
curl -fsSLo moji.tgz https://registry.npmjs.org/@microck/moji/-/moji-$VERSION.tgz
shasum -a 256 moji.tgz
brew reinstall --build-from-source ./Formula/moji.rb
brew test ./Formula/moji.rb
```

The formula lives in this repository, so users first run
`brew tap microck/moji https://github.com/Microck/moji` and then install it as
`microck/moji/moji`. The explicit URL is required because this repository is
not named `homebrew-moji`; no external tap repository or Homebrew/core change
is required.

The publish path is resumable. If npm publication succeeded but a later tag or
GitHub operation failed, rerun `npm run release:publish`. Moji compares npm's
published integrity with the newly verified archive, refuses any mismatch, and
continues from the first missing tag or release step. Existing remote tags must
be annotated and point to the current commit. Existing GitHub release assets
are downloaded and compared with the verified archive; a missing asset is
uploaded, while different bytes stop the run.

The generated binaries are intentionally ignored by Git. Before publishing,
Moji reads the VCS metadata embedded by Go and requires every binary to identify
the current clean commit. A stale binary, a different revision, or locally
modified source stops the release before npm, tags, or GitHub are changed.
