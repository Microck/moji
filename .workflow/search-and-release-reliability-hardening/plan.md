# Search and release reliability hardening

## Goal

Make Moji recover from bad ranked candidates, remember invalid direct URLs,
rank reliable sources consistently, and make stale-binary npm releases
impossible through one repository-owned release runbook.

## Success Criteria

- Single-file `moji get` tries ranked candidates until one downloads and
  validates, then reports an actionable aggregate error only when all fail.
- Family downloads preserve family/weight selection semantics and never leave
  partial output without clearly reporting the outcome.
- Validation failures enter a bounded, expiring negative URL cache used by
  later searches without adding eager downloads to the search hot path.
- Equal-relevance results prefer structured registries, then raw GitHub,
  GetFonts, and arbitrary web hosts, independently of license/trust metadata.
- A canonical release command rebuilds all six targets, packs the exact npm
  tarball, extracts it, verifies manifest/native/JS versions, forces lifecycle
  scripts, and gates npm/tag/GitHub publication on those checks.
- CI runs the package artifact/version verification needed to prevent the
  `v0.2.0` stale-binary incident.
- Contracts, CLI output, documentation, and tests describe the behavior.
- The 21-font corpus is repeated with direct-result and byte-validation
  evidence, without accepting pages or bypassing gated sources.

## Current Context

Moji v0.2.1 is the latest corrected release. The prior v0.2.0 package reused
stale binaries because machine-level `ignore-scripts=true` skipped `prepack`.
The 21-font corpus found 19 search hits but only 18 byte-valid direct fonts;
Knockout was invalid, Mrs. Eaves had an invalid result ahead of a valid raw
GitHub file, and Archer plus Belarius Serif Narrow had no direct source.

## Constraints

- Keep direct HTTPS files or safe archive members as the result contract.
- Do not weaken magic/structural validation or bypass publisher gates.
- Keep search latency bounded; do not download every candidate eagerly.
- Keep source reliability separate from legal trust and license metadata.
- Preserve Linux, macOS, and Windows support on x64 and arm64.
- No external publish, tag, release, or merge without explicit approval.

## Risks

- Retry logic could download unwanted extra family members or leave partial
  files after a later failure.
- Negative caching could suppress a URL after a transient or repaired failure.
- Reliability weights could overpower family relevance.
- Release automation could publish after a partial verification or use the
  wrong commit/tag target.

## Approval Required

Local implementation, fixtures, dry runs, and read-only corpus searches are
approved by the goal request. Any real npm publish, GitHub tag/release, merge,
or destructive cleanup requires a separate explicit approval.

## Work Packets

1. Define fallback, failure aggregation, URL-health, and reliability contracts.
2. Implement ranked single-file and family-aware download fallback.
3. Implement bounded negative URL health caching and ranking integration.
4. Implement source-reliability scoring with regression coverage.
5. Implement the repository-owned release runbook and artifact verifier.
6. Add CI enforcement and progressive-disclosure documentation.
7. Run security review, full verification, and the 21-font corpus.

## Integration Policy

Keep validation in the downloader boundary. Feed its typed failure outcome into
URL health, then use health and reliability only as ranking inputs. Release
automation must invoke one verifier shared with CI rather than duplicating
checks in prose or shell fragments.

## Verification

- Focused unit tests for fallback ordering, aggregate errors, TTL/eviction,
  source precedence, and family semantics.
- HTTP integration fixtures with invalid-first/valid-second candidates.
- Package tarball fixture that proves stale embedded versions fail closed.
- Linux race suite plus Windows amd64 and macOS arm64 cross-builds.
- `npm run verify`, production docs build, and workflow artifact verification.
- Repeat the 21-font corpus and separately report search hits and validated
  downloads.

## Reusable Artifacts

Keep the release verifier and runbook in the repository for every future
release. Preserve a concise corpus procedure and expected classifications in
the workflow results without committing proprietary font files.
