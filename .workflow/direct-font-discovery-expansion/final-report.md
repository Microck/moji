# Final Report: Direct font discovery expansion

## Outcome

Implemented all six requested discovery mechanisms behind one direct-file
contract. Search results remain downloadable HTTPS font files or safe members
of supported archives. Webpages and gated download flows are not promoted to
results.

## Accepted Results

- ZIP, TAR, and TGZ members with safe paths and configured font formats.
- CSS `url(...)` sources with a supported extension or declared `format(...)`.
- Raw GitHub tree files and direct release font/archive assets.
- Fontsource variant URLs and Google Fonts stylesheet URLs.
- Version 1 plugin responses that resolve through the same discovery boundary.

## Rejected Results

- HTML pages, insecure URLs and redirect downgrades, unsupported formats, and
  malformed plugin responses.
- Unsafe archive paths, excessive entry counts, oversized members, and
  archives over the expanded-size limit.
- Sources that expose only a gated publisher flow rather than a direct file.

## Conflicts Resolved

- GitHub Code Search remains authenticated, while bounded repository, tree,
  and release discovery uses GitHub's unauthenticated REST path when no token
  is configured.
- Kagi remains an internal `websearch` backend rather than a public provider.
- Structured catalogs are exposed as one public `registry` provider.

## Verification Evidence

- Focused unit and HTTP integration tests cover ZIP/TAR/TGZ, extraction,
  stylesheet resolution, insecure redirects, GitHub trees/releases, both
  registries, adaptive query variants, and a real plugin subprocess.
- `CGO_ENABLED=1 go test -race -count=1 ./...` passed.
- `go vet ./...` passed.
- `npm run verify` passed Go tests, docs type generation, TypeScript, ESLint,
  the production Next.js build, and 51 generated static pages.
- A no-Kagi, no-token live corpus returned direct results for Operator Mono
  and MonoLisa. PragmataPro, Catedra, CSTM Xprmntl, Nida, and Loes still had no
  legal public direct result in the enabled sources during the run.

## Remaining Risks

- Public availability is not guaranteed for proprietary fonts. Source plugins
  provide an extension point but do not bypass publisher access controls.
- GitHub's unauthenticated request allowance is small; the TUI recommends a
  token for Code Search and higher limits.
- License metadata is best effort and remains `unknown` when the source does
  not provide a specific license identifier.

## Reusable Follow-up

The source plugin request/response contract is documented at
`docs/content/docs/reference/source-plugins.mdx`. New structured sources can
also be added behind the existing `Provider` interface and discovery resolver.
