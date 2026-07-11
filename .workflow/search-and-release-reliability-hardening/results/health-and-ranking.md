# URL health and reliability ranking result

Status: implemented, focused verification passing

- Invalid-content URLs persist for 30 days in a 256-entry bounded cache.
- Search and download lookups are local-only and do not fetch candidates.
- Only typed byte-validation failures are recorded.
- Structured registries and raw GitHub win quality ties over GetFonts and
  arbitrary hosts without changing trust or license metadata.
- Focused command: `go test ./internal/cache ./internal/rank ./internal/app`.
