# Final Report: Search and release reliability hardening

## Outcome

Moji now continues through ranked candidates until it reaches the download
cap or exhausts the pool. Entire-family requests stage and validate one
provenance-coherent group before moving files. Invalid direct content is
remembered in a bounded, expiring, crash-safe cache shared across CLI and TUI
downloads. Ranking uses source reliability only after relevance and quality.

The repository also owns a resumable release command that rebuilds and checks
all six native targets, forces lifecycle scripts, verifies the exact npm
archive, and repairs a partially completed npm/tag/GitHub publication only
when artifact integrity and commit identity still match.

## Accepted Results

- Ranked invalid-first fallback with aggregate errors.
- Staged family fallback using repository, stylesheet, archive, or direct-URL
  provenance rather than display hostname.
- 30-day, 256-entry URL-health cache with atomic replacement and a bounded
  cross-process update lock.
- Registry, raw GitHub, GetFonts, arbitrary-host reliability tie-break order.
- Verify-only and explicitly publishing release modes with CI enforcement.
- Reproducible 21-font corpus runner and checked TSV evidence.

## Rejected Results

- HTML pages and invalid magic bytes remain rejected.
- Network, HTTP, policy, cancellation, and filesystem failures do not poison
  URL health.
- Archer and Belarius Serif Narrow Regular remain misses rather than being
  replaced by a page or gated download.

## Conflicts Resolved

- `--max` remains a cap on successful files while the fallback candidate pool
  stays untruncated.
- Family grouping remains coherent even when several repositories share a CDN
  hostname.
- Source reliability remains separate from `trusted` and `license`.
- A release retry never tags local bytes that differ from the immutable npm
  archive and never accepts a conflicting remote tag or GitHub asset.

## Verification Evidence

- `CGO_ENABLED=1 go test -race ./cmd/... ./internal/... ./e2e/...`: 130 tests.
- `npm run test:release`: 5 tests.
- `npm run release`: six binaries rebuilt, packed, extracted, and verified.
- `npm run verify`: Go vet/tests plus production documentation verification.
- `git diff --exit-code -- binaries`: committed binaries match rebuilt output.
- `npm run corpus:verify`: 19 of 21 byte-valid direct downloads; 25 invalid
  candidates rejected and remembered.

## Remaining Risks

- Live font availability is external and can change after the captured corpus.
- Multi-file filesystem moves cannot be one kernel transaction. Moji validates
  before moving and rolls back completed moves; if rollback itself fails, the
  error now identifies possible partial output instead of claiming success.

## Reusable Follow-up

- Run `npm run corpus:verify -- --output-dir <directory>` to regenerate JSON
  and TSV corpus evidence.
- Run `npm run release` for a non-publishing archive verification.
- Run `npm run release:publish` only after committing a version bump and all
  six rebuilt binaries.
