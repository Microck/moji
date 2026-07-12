# CLI Font Conversion Plan

## Goal

Add self-contained local font conversion to the Moji CLI without exposing the
feature in the TUI or changing provider search and ranking behavior.

Success means Moji can convert TTF or OTF fonts to WOFF2 and restore WOFF2
fonts to their intrinsic TTF or OTF flavor, while preserving the repository's
cross-platform, no-cgo binary distribution and file-safety guarantees.

## Product contract

```text
moji convert <input> [--to ttf|otf|woff2] [-o <output>] [--json]
```

Examples:

```bash
moji convert Inter.ttf
moji convert Inter.otf --to woff2
moji convert Inter.woff2
moji convert Inter.woff2 --to otf -o build/Inter.otf
moji convert Inter.ttf --json
```

Supported conversions:

| Input | Output |
| --- | --- |
| TTF | WOFF2 |
| OTF/CFF | WOFF2 |
| WOFF2 containing TrueType outlines | TTF |
| WOFF2 containing CFF outlines | OTF |

The command changes the OpenType container flavor. It does not rewrite glyph
outlines. Requests such as TTF-to-OTF and OTF-to-TTF must fail instead of
approximating the conversion.

`--to` is optional when the target is unambiguous:

- TTF and OTF inputs default to WOFF2.
- WOFF2 input defaults to the TTF or OTF flavor recorded in its embedded
  `sfntVersion`.

Input format is detected from file bytes rather than the filename extension.

### Output rules

- The default output is beside the input with the target extension.
- `-o` and `--output` select an explicit output path.
- Existing files are never overwritten.
- Plain success output is `Converted: <path>` on stdout.
- `--json` prints stable input path, output path, source format, target format,
  byte size, and SHA-256 fields on stdout.
- Diagnostics and errors go to stderr.
- Exit code `2` means invalid syntax or an unsupported conversion pair.
- Exit code `1` means invalid font data, a codec failure, or a filesystem
  failure.

The first version does not support stdin, binary stdout, batching, `--force`,
configuration keys, WOFF1, font collections, or conversion from `moji get`.

## Research decisions

FontTools is the behavioral reference. Its WOFF2 implementation compresses
either TrueType- or CFF-flavored OpenType into WOFF2 and restores the embedded
flavor during decompression. It selects `.otf` when `sfntVersion` is `OTTO`
and `.ttf` for TrueType-flavored SFNT data. It does not convert one outline
technology into the other.

Reference:

- https://github.com/fonttools/fonttools/blob/main/Lib/fontTools/ttLib/woff2.py

FontTools must not become a runtime dependency. Moji ships six self-contained
binaries with `CGO_ENABLED=0`; requiring Python, FontTools, and Brotli on the
user's machine would break that distribution model.

The preferred runtime codec is `github.com/pgaskin/go-woff2` because it offers
both encoding and decoding without cgo:

- https://github.com/pgaskin/go-woff2
- https://github.com/pgaskin/go-woff2/blob/master/woff2.go

This dependency has a mandatory qualification gate. Version 0.0.2 is new, has
one primary contributor, contains generated codec code, and requires Go
1.26.3. At planning time, Moji declared Go 1.25.0. The implementation must not
add a Python fallback, cgo helper, compatibility shim, or second codec path if
this dependency fails qualification.

Qualification completed on 2026-07-12:

- `go-woff2` v0.0.2 passed TTF and OTF interoperability against FontTools
  4.63.0 in both directions.
- All six Moji targets built with `CGO_ENABLED=0` under Go 1.26.3.
- The packed npm archive grew from the older 18.6 MB baseline to 28.9 MB.
  This is the cost of embedding one self-contained codec in every platform
  binary; no runtime or fallback dependency was introduced.
- Codec, Brotli, WOFF2, and FontTools fixture license notices are preserved in
  `third-party-notices.md`.

## Implementation plan

### 1. Establish the contract and failing tests

- Update `docs/content/docs/reference/cli.mdx` with the command grammar,
  conversion matrix, inferred targets, naming behavior, JSON schema, and
  collision policy.
- Update `docs/content/docs/reference/errors-and-exit-codes.mdx` with
  conversion usage and operational failures.
- Add failing application tests for parsing, inferred targets, explicit
  targets, unsupported pairs, help, JSON output, collisions, and exit codes.

The documentation is the contract artifact and must be updated before the
implementation is made green.

### 2. Qualify and pin the codec

- Pin `github.com/pgaskin/go-woff2` at an exact version in `go.mod` and
  `go.sum`.
- Move Moji to the minimum compatible Go 1.26 toolchain.
- Verify all six release targets still build with `CGO_ENABLED=0`.
- Measure the resulting native binary and npm archive size changes.
- Audit the generated-code provenance and all transitive license obligations.
- Stop and report the blocker if cross-compilation, licensing, or package
  growth is unacceptable.

### 3. Add a focused conversion module

- Create `internal/fontconvert/fontconvert.go`.
- Detect TTF, OTF, and WOFF2 from signatures and embedded `sfntVersion`.
- Encode TTF/OTF to WOFF2 and decode WOFF2 to its intrinsic TTF/OTF flavor.
- Reject same-format conversion, outline-changing pairs, collections,
  malformed input, truncated input, and oversized input with actionable
  errors.
- Return a typed conversion record used by both plain and JSON presentation.
- Keep validation at the conversion boundary rather than repeating it in the
  CLI layer.

### 4. Preserve atomic, no-overwrite file semantics

- Extract the existing cross-platform `moveNoReplace` primitive from
  `internal/download` into a small shared internal file-commit package.
- Keep the download package's current behavior unchanged through regression
  tests.
- Write converted bytes to a temporary file in the destination directory,
  close and validate it, then commit it without replacing an existing path.
- Remove temporary residue after every failure.

### 5. Wire the CLI without touching the TUI

- Dispatch `convert` in `internal/app/app.go` before provider configuration is
  loaded. Local conversion must work even when the provider config is missing
  or malformed.
- Add a dedicated conversion parser and runner in `internal/app/convert.go`.
- Do not add conversion fields to the existing search `options` type.
- Add `moji convert --help` and update the global help text in
  `internal/app/output.go`.
- Do not modify files under `internal/tui`.

### 6. Add codec and command verification

- Add small, license-compatible TTF, OTF, and WOFF2 fixtures under
  `internal/fontconvert/testdata`.
- Test both conversion directions, intrinsic-flavor detection, invalid and
  truncated inputs, explicit output paths, collisions, output cleanup, and
  size limits.
- Add a binary-level round trip to `e2e/cli_test.go`.
- Add a FontTools conformance lane that proves FontTools can decode Moji output
  and Moji can decode a FontTools-generated WOFF2 fixture.
- Inspect fixture licenses and record their provenance next to the fixtures.

### 7. Document discoverability

- Add one conversion example and the new command to `README.md`.
- Update `docs/content/docs/explanation/architecture.mdx` to show conversion as
  a local-file pipeline separate from providers, downloads, and the TUI.
- Do not add configuration keys or change default search formats.

### 8. Run final verification

Run the repository's established verification paths:

```bash
go vet ./cmd/... ./internal/... ./e2e/...
go test ./cmd/... ./internal/... ./e2e/...
npm run build:binaries
npm run docs:check
npm run docs:build
npm run release
npm run verify
```

Inspect the final `jj diff`, confirm the verified npm archive contains all six
generated native binaries, and confirm no TUI source files changed.

## Completion criteria

The work is complete only when:

1. Every supported conversion succeeds through the real Moji binary.
2. Unsupported outline-changing conversions fail without output residue.
3. Existing destinations are never replaced.
4. Plain and JSON output match the documented contracts.
5. FontTools interoperability passes in both directions.
6. All six `CGO_ENABLED=0` release targets build successfully.
7. Go, documentation, release, and full repository verification pass.
8. No conversion behavior or controls are added to the TUI.

If the same external blocker prevents the codec qualification in two
consecutive goal continuations, stop the implementation goal and report the
blocker rather than introducing a fallback path.

## Out of scope

- Post-download conversion through `moji get`
- TUI conversion controls
- TTF-to-OTF or OTF-to-TTF outline conversion
- WOFF1, EOT, Type 1, dfont, TTC, or OTC conversion
- Subsetting, hinting changes, variable-font instancing, or table editing
- Runtime Python, FontTools, cgo, external helper binaries, or codec fallbacks
