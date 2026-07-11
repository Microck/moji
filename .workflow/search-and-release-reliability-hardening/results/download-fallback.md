# Download fallback result

Status: implemented, focused verification passing

- Added a typed invalid-content failure boundary for safe URL-health recording.
- Kept the full ranked candidate pool available to `moji get`.
- Added ranked retry until the requested success count is reached.
- Added aggregate candidate errors.
- Added staged, same-source family fallback through `DownloadBatch`.
- Focused command: `go test ./internal/app ./internal/download`.
