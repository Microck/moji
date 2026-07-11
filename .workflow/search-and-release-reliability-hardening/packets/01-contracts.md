# Packet 01: behavioral contracts

Status: completed

## Download fallback

- `--max` caps successful downloads; it does not cap the candidates Moji may try.
- Ranked candidates remain ordered. A failed candidate does not hide later candidates.
- A family request tries one normalized family and source as a unit. It never mixes sources in one family result.
- Family grouping uses stable repository, stylesheet, archive, or direct-URL provenance rather than a display hostname alone.
- Family files are validated in staging and become visible only after every selected member validates.
- If no candidate succeeds, the error names every attempted candidate or family source.

## URL health

- Only a completed response whose bytes fail font validation is a negative URL-health observation.
- HTTP, network, cancellation, policy, and local filesystem failures do not poison URL health.
- Health observations expire and the store has a fixed maximum size.
- Applying health state does not make network requests and does not alter trust or license metadata.

## Source reliability

- Reliability is a ranking heuristic separate from `trusted` and `license`.
- Query relevance and coherent family completeness remain stronger ordering constraints.
- At otherwise comparable quality, structured registries and raw GitHub rank above GetFonts and arbitrary web hosts.

## Release gate

- Release verification rebuilds all six native targets and inspects the exact packed tarball.
- Manifest, JavaScript launcher, and native binary versions must match before publish.
- Lifecycle scripts are explicitly enabled even when the user's npm configuration disables them.
- Publishing precedes tag and GitHub release creation. Verification failure performs none of those external mutations.
