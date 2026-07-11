# Release runbook result

Status: complete

Implemented a repository-owned TypeScript release command with verification-only
default behavior and an explicit publish mode. The command rebuilds all six
native targets, forces npm lifecycle scripts during packing, extracts the exact
tarball, verifies manifest, binary target/version, and JavaScript launcher
versions, then gates npm publish, annotated tag creation, tag push, and GitHub
release creation behind successful verification.

Publication is resumable. An existing npm version must match the verified
archive integrity, a remote tag must be annotated and point to the current
commit, and an existing GitHub asset must contain the same archive bytes.

Verification:

- `npm run test:release`
- `npm run release`
- `npm run docs:check`

No package, tag, push, commit, or GitHub release was created.
