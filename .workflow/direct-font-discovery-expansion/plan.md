# Direct font discovery expansion

## Goal

Increase Moji's verified direct-font success rate through six integrated discovery mechanisms without treating HTML pages as font results or bypassing access gates.

## Success Criteria

- Direct ZIP/TAR archives can be inspected safely and selected font members can be downloaded and validated.
- CSS and `@font-face` sources yield validated direct font URLs.
- GitHub discovery can inspect relevant repository trees and release assets after bounded search discovery.
- At least two structured public font registries are integrated with direct-file contracts.
- Adaptive query rounds broaden only after exact/family searches fail and remain request-bounded.
- External source plugins have a documented JSON protocol, configuration, validation, and failure isolation.
- Existing CLI, TUI, table, JSON, cache, and family-selection behavior remains coherent.
- Race tests, vet, repository verification, docs build, and focused live searches pass.

## Current Context

Moji currently has GetFonts, authenticated GitHub Code Search, and a combined websearch provider with Kagi CLI and optional SearXNG backends. Search results must be direct supported font files. The working copy already contains uncommitted search, format, ranking, and TUI improvements that must be preserved.

## Constraints

- No webpage-only results.
- No gated-flow bypasses or fabricated license claims.
- Preserve direct-file magic/structural validation.
- Bound network requests, archive sizes, expansion ratios, recursion, and redirects.
- Support Linux, macOS, and Windows on amd64 and arm64.
- Do not commit or publish without explicit instruction.

## Risks

- Archive traversal, zip bombs, decompression bombs, and ambiguous members.
- SSRF or unbounded crawling during CSS discovery.
- GitHub and web-search rate exhaustion.
- Plugin executable trust and malformed output.
- Paid/proprietary fonts with no authorized public direct source.

## Approval Required

No additional approval for local implementation and read-only live verification. Publishing, committing, or bypassing publisher access controls remains unauthorized.

## Work Packets

1. Core result/source contracts: archive members, registry metadata, plugin protocol.
2. Safe archive inspection and downloader extraction.
3. CSS discovery and URL safety boundary.
4. GitHub tree/release discovery and adaptive query planner.
5. Structured registry providers.
6. Plugin loading, process isolation, and validation.
7. Integration, docs, live corpus, security audit, and full verification.

## Integration Policy

Add the smallest shared contracts first. Providers may emit only results that the downloader can validate. Merge by canonical direct source plus archive member. Exact family relevance stays above discovery breadth and completeness scoring.

## Verification

- Focused unit tests for pure parsers and safety limits.
- `httptest` integration tests for HTTP/provider boundaries.
- Real subprocess fixture for plugins.
- `go test -race ./...`, `go vet ./...`, and `npm run verify`.
- Live searches covering common, obscure, archive-distributed, CSS-hosted, and legacy fonts.

## Reusable Artifacts

Document the source-plugin protocol and provider safety contract under the existing docs reference tree.
