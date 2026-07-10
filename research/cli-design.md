# moji — Design Document

文字 (moji, "characters/type") — a cozy terminal font finder.

## Brand

- **Name:** moji (文字)
- **Logo:** The kanji 文字
- **Color:** Orange (accent throughout the TUI — prompts, highlights, mascot)
- **Vibe:** Same cozy terminal feel as tork, but for fonts

Reference: [tork](https://github.com/melqtx/tork) (melqtx/tork) — a terminal torrent search TUI in Go + Bubble Tea. We adapt its architecture for font search.

## Current provider contract

The provider plan below records the original research, but live verification
changed the runnable provider set:

- GetFonts is the zero-configuration default and uses its current
  `/api/search?q=...` JSON endpoint.
- GitHub Code Search requires authentication. It is registered only when
  `GITHUB_TOKEN` or `github_token` is available.
- Web search is opt-in and is registered only when a SearXNG instance is
  configured.
- getthefont.com is unreachable and is not shipped as a provider. Moji does
  not expose a knowingly broken provider.

This current contract takes precedence over historical phase descriptions
later in this document.

Only settings enforced by the current binary are part of the configuration
contract. The historical `preview_before_download`, `max_concurrent`, and
multi-engine web search examples below are not exposed because they do not map
to independent working behavior.

## What tork Does Well (and what we steal)

tork is structured around clean separable packages, each independently testable:

| tork package | Purpose | moji equivalent |
|---|---|---|
| `provider` | Provider interface + per-source implementations (Knaben, YTS, Nyaa, EZTV, 1337x, RSS) | GitHub Code Search, getthefont.com, getfonts.cc, web search dorking |
| `aggregator` | Fan-out/fan-in: queries all providers concurrently, streams results back with status events | Same — query all font sources concurrently |
| `rank` | Normalize release names → parse tags (resolution, source, codec) → score with configurable weights | Normalize font filenames → parse tags (format, weight, family completeness) → score |
| `tui` | Bubble Tea screens: search → results → preview → downloads | Same flow: search → results → preview (glyph rendering) → download |
| `autopilot` (`get`) | Natural language intent → parse → search → rank → pick → queue | "proxima nova bold" → parse → search → rank → download best match |
| `config` | YAML config in `~/.tork/config.yaml` with provider toggles, ranking weights, download dir | `~/.moji/config.yaml` with GitHub token, provider toggles, ranking weights |
| `state` | Persists active downloads in `state.json` | Optional — track download history, dedupe cache |
| `engine` | Torrent download engine (anacrolix/torrent) | Direct HTTP download — much simpler, no P2P needed |

## Architecture

```
~/workspace/moji/
├── cmd/moji/main.go                 # entry point, flag parsing
├── internal/
│   ├── provider/
│   │   ├── provider.go              # Provider interface, Result struct, shared HTTP utils, typed errors
│   │   ├── github.go                # GitHub Code Search API provider
│   │   ├── getthefont.go            # getthefont.com scraper
│   │   ├── getfonts.go              # getfonts.cc scraper
│   │   └── websearch.go             # Fallback: dork queries via search API
│   ├── aggregator/
│   │   └── aggregator.go            # Fan-out/fan-in, status events, retries, rate policies
│   ├── rank/
│   │   ├── normalize.go             # Normalize font filenames to canonical names
│   │   ├── tags.go                  # Parse format/weight/family from filename
│   │   ├── family.go                # Group individual results into family candidates
│   │   └── score.go                 # Score results with configurable weights
│   ├── download/
│   │   └── download.go              # Safe download: magic byte validation, atomic rename, hash dedup
│   ├── config/
│   │   └── config.go                # YAML config in ~/.moji/ (0600 permissions)
│   └── tui/
│       ├── app.go                   # Bubble Tea app, screen routing
│       ├── search.go                # Search input screen
│       ├── results.go               # Results list with sorting/filtering
│       ├── preview.go               # Glyph preview (optional, render sample text)
│       └── mascot.go                # 文字 logo + orange styling
└── go.mod
```

## Language: Go

Same as tork. Reasons:
- Bubble Tea is the best TUI framework available — model-update-view, composable
- Goroutines + channels are a natural fit for fan-out search
- Single binary distribution (no runtime deps, no pip, no node_modules)
- tork's code is directly portable as reference

## Provider Interface

Directly adapted from tork, with typed events for robust partial-failure handling.

### Typed errors

```go
var (
    ErrRateLimited = errors.New("rate limited")
    ErrBlocked     = errors.New("blocked by site protection")
    ErrUnavailable = errors.New("provider unavailable")
    ErrBadResponse = errors.New("bad response from provider")
)
```

### Event model

Instead of streaming bare `Result` values, providers emit typed events. This decouples the TUI/aggregator from provider internals and gives retry logic a stable contract.

```go
type EventType int

const (
    EventResult EventType = iota   // a result was found
    EventStatus                     // searching / done / failed
)

type ProviderEvent struct {
    Provider   string
    Type       EventType
    Result     Result              // set when Type == EventResult
    Status     ProviderState       // set when Type == EventStatus
    Err        error               // set when Status == StateFailed
    RetryAfter time.Duration       // set when Err is ErrRateLimited
    Count      int                 // results emitted so far (status events)
}

type ProviderState int

const (
    StateSearching ProviderState = iota
    StateDone
    StateFailed
    StateThrottled
)
```

### Result struct

```go
type Result struct {
    Name       string  // "Proxima Nova"
    Filename   string  // "ProximaNova-Bold.otf"
    Format     string  // "otf", "ttf", "woff2", "woff"
    Weight     string  // "Bold", "Regular", "Light", etc.
    SizeBytes  int64
    Source     string  // "github.com/user/repo", "getthefont.com"
    URL        string  // direct download URL
    Trusted    bool    // source flagged as reliable
    License    string  // license hint: "OFL", "MIT", "unknown" (best-effort from repo)
}
```

### Provider contract

```go
type Provider interface {
    Name() string
    Search(ctx context.Context, query string, out chan<- ProviderEvent) error
}
```

### Providers to implement

**1. GitHub Code Search (primary — covers ~80% of finds)**

```
GET /search/code?q=filename:ProximaNova+extension:otf
GET /search/code?q=filename:ProximaNova+extension:ttf
GET /search/code?q=filename:ProximaNova+extension:woff2
```

Queries are built from the `--format` flag (default: all formats). See [Format Filtering](#format-filtering).

Requires a GitHub token. Without one, the API is severely rate-limited (60 req/hr per IP). With a token: 5,000 req/hr — effectively unlimited for normal use.

Token resolution (checked in this order):

1. **`GITHUB_TOKEN` env var** (recommended):
   ```
   export GITHUB_TOKEN=ghp_xxxxxxxxxxxx
   moji "Proxima Nova"
   ```

2. **Config file** (`~/.moji/config.yaml`, permissions `0600`):
   ```yaml
   github_token: ghp_xxxxxxxxxxxx
   ```

3. **`--token-stdin`** (piped, never appears in shell history):
   ```
   echo "ghp_xxxxx" | moji "Proxima Nova" --token-stdin
   ```

Tokens are redacted from all logs, debug output, and error messages. The `--token` inline flag is intentionally omitted to prevent shell history leaks.

How to get a token: GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens → Generate new (public read access is enough, no scopes needed for public code search).

If no token is found, moji still works but falls back to unauthenticated rate limits and may hit the 60 req/hr cap during heavy use.

For each result, construct raw URL:
```
https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}
```

**2. getthefont.com** — scrapes GitHub repos that the API might miss. HTTP only (no HTTPS). Results flagged as untrusted with a warning in `--verbose` mode.

**3. getfonts.cc** — another aggregator.

**4. Web search fallback** — dork queries through a search API, parse for direct font file links. **Disabled by default** — requires explicit opt-in. Result caps applied, stricter download validation, warnings for unknown direct-link sources.
```
site:github.com "FontName".ttf
site:vk.com "FontName".ttf
"FontName" indexof:.ttf
```

## Format Filtering

The `--format` flag (or `-f`) filters results to specific font file types:

```
moji "Proxima Nova"                          # all formats (default)
moji "Proxima Nova" --format otf             # OTF only
moji "Proxima Nova" -f ttf                   # TTF only
moji "Proxima Nova" -f otf,ttf               # OTF and TTF
moji "Proxima Nova" -f woff2                 # WOFF2 only (web fonts)
moji get "proxima nova bold" -f otf          # get mode with format filter
```

Supported formats: `otf`, `ttf`, `woff`, `woff2`

How it works:
- **GitHub provider:** the `extension:` query parameter is set to only the requested formats, reducing API calls. E.g. `--format otf` → `extension:otf` only (1 API call instead of 4).
- **Scrapers:** results are filtered client-side after fetching.
- **Config default:** `default_formats: [otf, ttf, woff2]` in config.yaml lets you always exclude formats you don't want.
- **TUI:** the format filter is shown as a header badge, and `f` cycles through format presets (all → otf → ttf → woff2 → all).

## Rate Policies

Each provider has a `RatePolicy` governing concurrency, timeouts, retries, and backoff:

```go
type RatePolicy struct {
    MaxConcurrent int           // max parallel requests to this provider
    Timeout       time.Duration // per-request timeout
    Retries       int           // max retry attempts on transient failure
    BackoffBase   time.Duration // initial backoff (doubles each retry)
    BackoffJitter time.Duration // random jitter added to backoff
}
```

Default policies:

| Provider | MaxConcurrent | Timeout | Retries | Notes |
|---|---|---|---|---|
| GitHub | 2 | 15s | 2 | Respects `Retry-After` header on 403/429 |
| getthefont | 1 | 15s | 1 | HTTP only, single request |
| getfonts | 1 | 15s | 1 | |
| websearch | 1 | 20s | 0 | No retries — fragile by design |

When a provider is throttled (`StateThrottled`), the TUI shows a "⏳ github: rate limited (retrying in 30s)" indicator instead of silently failing.

## Download Safety

Downloads are the riskiest part of the app — fetching arbitrary public files from untrusted URLs. The downloader enforces:

1. **HTTPS preferred** — HTTP only for explicit opt-in providers (getthefont). `--allow-insecure` required to download over HTTP.
2. **Magic byte validation** — verify the downloaded file matches its claimed format:
   - TTF: `00 01 00 00` or `OTTO` (OpenType with TrueType outlines)
   - OTF: `4F 54 54 4F` ("OTTO")
   - WOFF: `77 4F 46 46` ("wOFF")
   - WOFF2: `77 4F 46 32` ("wOF2")
3. **Max file size** — default 50MB. Fonts are never this large; reject anything bigger.
4. **Temp-file + atomic rename** — download to `.tmp` file, validate, then atomic rename to final path. Partial/corrupt files never land at the destination.
5. **Sanitized filenames** — strip path components, use only the basename.
6. **Redirect cap** — max 5 redirects (same as tork's provider).
7. **SHA-256 hash** — computed on download, used as dedup key. Same font from two sources = one file on disk.

## Family Grouping

A `ResultGroup` stage sits between provider aggregation and ranking. Individual files are grouped into family candidates before scoring:

```go
type ResultGroup struct {
    FamilyName  string   // canonical normalized name: "proxima nova"
    Source      string   // "github.com/user/repo" or "getthefont.com"
    Files       []Result // all matching files from this source
    Weights     []string // ["regular", "bold", "light"] extracted from files
    Formats     []string // ["otf", "ttf"] available formats
    BestFormat  string   // highest-ranked format available
    FileCount   int
}
```

Grouping key: `canonicalName + source + repo/path neighborhood`.

Scoring then operates on groups first (family completeness bonus), then ranks individual files within each group. This makes "entire family" and "all weights" mode reliable from the start.

## Rank / Score

tork normalizes torrent release names and scores by seeders/quality/trust. We adapt this for fonts.

### Normalize

Strip filename noise down to canonical font name + weight:

```
"ProximaNova-Bold.ttf"          → "proxima nova", weight: "bold"
"Proxima-Nova-Regular.otf"      → "proxima nova", weight: "regular"
"HelveticaNeueLTStd-Light.otf"  → "helvetica neue lt std", weight: "light"
"font-awesome-webfont.woff2"    → "font awesome webfont", weight: ""
```

Regex pipeline (like tork's `normalize.go`):
- Strip file extension
- Replace hyphens/underscores/dots with spaces
- Extract trailing weight token (Bold, Regular, Light, Medium, Semibold, Demi, Book, Condensed, etc.)
- Lowercase + collapse whitespace

### Tags / Parse

```go
type Tags struct {
    Format       string  // "otf", "ttf", "woff2", "woff"
    Weight       string  // "thin", "light", "regular", "medium", "semibold", "bold", "black"
    Italic       bool
    FamilyMember bool    // part of a family (has weight qualifier)
}
```

### Score

```go
type Weights struct {
    Format       float64  // prefer otf > ttf > woff2
    FamilySize   float64  // bonus for more weights found in same source
    Trusted      float64  // bonus for known-good sources
    SizePenalty  float64  // penalize suspiciously small files
    WeightBonus  float64  // bonus if specific weight was requested and matched
}
```

Scoring formula (adapted from tork's logarithmic approach):
```
score = w.Format * formatRank(format)         // otf=3, ttf=2, woff2=1
      + w.FamilySize * log2(1 + weightsFound)
      + w.Trusted * trustBoost(source)
      + w.WeightBonus * weightMatch(query, tags)
      - w.SizePenalty * suspiciousSize(sizeBytes)
```

## Aggregator (fan-out/fan-in)

Lifted almost verbatim from tork. Query all providers concurrently:

```
                    ┌→ GitHub Code Search ─┐
query → aggregator ─┼→ getthefont.com    ──┼→ merged events channel → TUI
                    ├→ getfonts.cc        ─┤
                    └→ web search         ─┘
```

- Each provider runs in its own goroutine
- Results stream in as they arrive (live updating TUI)
- Per-provider status events: `searching` → `done (N results)` / `failed (err)` / `throttled (retry in Xs)`
- Retry with backoff per `RatePolicy` (tork pattern)
- Skip retries on `ErrBlocked` (anti-bot) or cancelled context
- Respect `Retry-After` header on rate-limit responses
- `safeSearch` wraps each provider in `recover()` so one crash doesn't kill the app

## Caching

Results are cached to reduce API calls and speed up repeated searches:

- **Location:** `~/.cache/moji/`
- **Key:** query string + provider name + format filter
- **TTL:** 1 hour (configurable via `cache_ttl_seconds`)
- **Contents:** provider responses with fetch timestamp and result count
- **Controls:**
  - `--no-cache` — bypass cache for this search
  - `moji cache clear` — wipe cache

## Observability

Works in both TUI and non-TUI (table/JSON) modes:

- `--verbose` — show provider names, result counts, and timings
- `--debug` — full detail: API request/response summaries, retry decisions, rate-limit state, score breakdowns per result, cache hits/misses
- `--json` — machine-readable output (for scripting): array of results with all fields
- All modes redact tokens from output

## TUI (Bubble Tea)

Same screen flow as tork, with orange branding:

```
┌──────────────────────────────────────────┐
│  文字  moji                              │  ← orange logo + name
│                                          │
│  ❯ proxima nova                          │  ← search input (orange ❯)
│                                          │
│  Found 7 results:  [format: otf]         │  ← format filter badge
│                                          │
│  #  FORMAT  WEIGHT    SOURCE       SIZE  │  ← results list
│  1  OTF     Regular   github/...   142K  │     (sorted by score)
│  2  OTF     Bold      github/...   138K  │
│  3  TTF     Regular   getthefont   89K   │
│  4  WOFF2   Regular   github/...   31K   │
│  ...                                     │
│                                          │
│  ⏳ github: rate limited (retry in 30s)  │  ← throttle indicator
│                                          │
│  enter: preview  D: download  /: filter  │
│  f: format  o: sort                      │
└──────────────────────────────────────────┘
```

Keys (tork-style):
- **home** — type to search, `↑↓` browse, `enter` open results
- **results** — `enter` preview glyphs, `D` download, `/` filter, `o` sort
- `f` cycle format filter (all → otf → ttf → woff2 → all)
- `tab` cycle sort mode (score → format → size)
- `esc` back, `^c` quit

Preview screen renders sample text using the downloaded font (requires local font rendering — optional, can defer to opening in system font viewer).

### Orange palette

Lipgloss styles using orange as the brand accent:

```go
// Orange brand colors (ANSI truecolor via Lipgloss)
var styleBrand  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8C00")) // dark orange
var styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")) // orange
var styleFaint  = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
var styleFg     = lipgloss.NewStyle().Foreground(lipgloss.Color("#DDDDDD"))
```

Used for: logo (文字), prompt cursor (❯), highlighted result rows, status indicators.

## get (non-interactive mode)

tork's killer feature: `tork autopilot "all breaking bad seasons 1080p"`

moji equivalent:

```
moji get "proxima nova bold"
moji get "helvetica neue entire family"
moji get "ff meta serif regular" --dry-run
```

Intent parsing (adapted from tork's `intent.go`):
```go
type Intent struct {
    Query      string  // cleaned font name
    WantWeight string  // "bold", "regular", "" (any)
    WantFamily bool    // "entire family" / "all weights"
    Format     string  // "otf", "ttf", "" (any, prefer otf)
    Max        int     // max downloads
}
```

Parse → search → rank → select best per group → download (or `--dry-run` to preview picks).

## CLI Grammar

```
moji <query>                          # interactive TUI search
moji <query> [flags]                  # TUI with options pre-set
moji get <query> [flags]             # non-interactive: search → rank → download
moji config                           # open config in $EDITOR
moji config show                      # print current config
moji cache clear                      # clear results cache
moji --version
moji --help
```

Common flags (apply to both TUI and `get`):

| Flag | Short | Default | Description |
|---|---|---|---|
| `--format` | `-f` | all | Comma-separated: `otf,ttf,woff,woff2` |
| `--weight` | `-w` | any | Filter by weight: `bold`, `regular`, `light`, etc. |
| `--max` | `-n` | 10 | Max downloads (`get` mode) or max results (TUI) |
| `--provider` | | all | Restrict to specific providers: `github,getthefont` |
| `--json` | | off | Machine-readable JSON output |
| `--dry-run` | | off | Show what would be downloaded (`get` mode only) |
| `--download-dir` | `-d` | `~/Downloads/moji` | Download destination |
| `--verbose` | `-v` | off | Provider names, counts, timings |
| `--debug` | | off | Full detail: requests, retries, scores |
| `--no-cache` | | off | Bypass cache |
| `--token-stdin` | | off | Read GitHub token from stdin |
| `--allow-insecure` | | off | Allow HTTP downloads (getthefont) |

## Config

`~/.moji/config.yaml` (permissions `0600`):

```yaml
download_dir: ~/Downloads/moji
github_token: ""                      # GITHUB_TOKEN env var takes priority
preview_before_download: true
search_timeout_seconds: 15
cache_ttl_seconds: 3600
default_formats: [otf, ttf, woff2]   # default --format value

ranking:
  format: 3.0
  family_size: 2.0
  trusted: 1.5
  size_penalty: 0.5
  weight_bonus: 2.0

rate_limits:
  github:
    max_concurrent: 2
    timeout_seconds: 15
    retries: 2
  getthefont:
    max_concurrent: 1
    timeout_seconds: 15
    retries: 1

providers:
  github:
    enabled: true
  getthefont:
    enabled: true
  getfonts:
    enabled: true
  websearch:
    enabled: false                   # opt-in only
    engine: "searxng"                # or "brave"
    instance: ""
```

## Testing

### Golden tests first

Font name normalization and tag parsing are core to search quality. Write table-driven tests before implementing providers:

- `normalize_test.go` — all examples from the design doc + edge cases
- `tags_test.go` — weight extraction, italic detection, format parsing
- `score_test.go` — score ordering (given known inputs, verify rank order)
- `intent_test.go` — parse "proxima nova bold" → correct Intent struct

Edge cases to cover:
- Weight variants: `SemiBold`, `Semi-Bold`, `Demi`, `DemiBold`, `Book`, `Condensed`, `Compressed`, `Heavy`, `Ultra`
- Italic variants: `Italic`, `It`, `Oblique`
- Multi-word names: `Helvetica Neue LT Std`, `FF Meta Serif`
- Variable fonts: `Inter.var.ttf`, `[wght].ttf`
- Web fonts: `font-awesome-webfont.woff2` (no weight token)

### Scraper fixture tests

- Save HTML samples from getthefont.com and getfonts.cc into `provider/testdata/`
- Test parsing logic against saved fixtures
- Live network tests opt-in via `MOJI_LIVE_TESTS=1` env var

### Provider interface tests

- Mock providers that emit known event sequences
- Test aggregator fan-out/fan-in with mocked delays and failures
- Test retry logic with mocked rate-limit responses

## Implementation Phases

### Phase 1 — MVP (working CLI)
- `go.mod`, project scaffold, `.editorconfig`, Makefile/justfile
- `provider/github.go` — GitHub Code Search API
- `aggregator` — single provider for now
- `rank` — normalize, tags, score (with golden tests)
- `download` — safe download (magic bytes, atomic rename)
- `config` — YAML config with token resolution
- `cmd/moji/main.go` — CLI args, print results as table
- `--format`, `--weight`, `--json`, `--verbose`, `--debug` flags
- No TUI yet, just: `moji "Proxima Nova"` → table output

### Phase 2 — TUI
- Bubble Tea search → results → download flow
- Live result streaming as providers respond
- Format filter badge, throttle indicators
- 文字 logo, orange styling

### Phase 3 — More providers + smart features
- getthefont.com, getfonts.cc scrapers (with fixture tests)
- Web search fallback (opt-in)
- Family grouping layer
- Caching (`~/.cache/moji/`)

### Phase 4 — Polish
- `get` mode (`moji get "proxima nova bold"`)
- woff2 → ttf/otf conversion (behind interface, deferred unless dep chosen)
- Glyph preview (behind `Previewer` interface, deferred)
- Hash dedup across sources

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — styling
- [Bubbles](https://github.com/charmbracelet/bubbles) — text input, list, etc.
- [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) — config
- Standard library for HTTP, context, crypto/sha256

Preview and WOFF2 conversion are kept behind interfaces (`Previewer`, `Converter`) and deferred until specific dependencies are evaluated. This prevents Phase 4 complexity from leaking into the MVP architecture.

## Legal Note

Like tork, moji is a search client that hosts and indexes nothing. It queries public APIs and websites. Whether downloading a specific font is lawful depends on the font's license and the user's jurisdiction — the tool makes no distinction.

Results include best-effort provenance: source URL, repo name, and license hint when detectable. Users are responsible for verifying they have the right to use any downloaded font.
