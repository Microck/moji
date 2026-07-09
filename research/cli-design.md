# moji — Design Document

文字 (moji, "characters/type") — a cozy terminal font finder.

## Brand

- **Name:** moji (文字)
- **Logo:** The kanji 文字
- **Color:** Orange (accent throughout the TUI — prompts, highlights, mascot)
- **Vibe:** Same cozy terminal feel as tork, but for fonts

Reference: [tork](https://github.com/melqtx/tork) (melqtx/tork) — a terminal torrent search TUI in Go + Bubble Tea. We adapt its architecture for font search.

## What tork Does Well (and what we steal)

tork is structured around clean separable packages, each independently testable:

| tork package | Purpose | moji equivalent |
|---|---|---|
| `provider` | Provider interface + per-source implementations (Knaben, YTS, Nyaa, EZTV, 1337x, RSS) | GitHub Code Search, getthefont.com, getfonts.cc, web search dorking |
| `aggregator` | Fan-out/fan-in: queries all providers concurrently, streams results back with status events | Same — query all font sources concurrently |
| `rank` | Normalize release names → parse tags (resolution, source, codec) → score with configurable weights | Normalize font filenames → parse tags (format, weight, family completeness) → score |
| `tui` | Bubble Tea screens: search → results → preview → downloads | Same flow: search → results → preview (glyph rendering) → download |
|| `autopilot` (`get`) | Natural language intent → parse → search → rank → pick → queue | "proxima nova bold" → parse → search → rank → download best match |
| `config` | YAML config in `~/.tork/config.yaml` with provider toggles, ranking weights, download dir | `~/.moji/config.yaml` with GitHub token, provider toggles, ranking weights |
| `state` | Persists active downloads in `state.json` | Optional — track download history, dedupe cache |
| `engine` | Torrent download engine (anacrolix/torrent) | Direct HTTP download — much simpler, no P2P needed |

## Architecture

```
~/workspace/moji/
├── cmd/moji/main.go                 # entry point, flag parsing
├── internal/
│   ├── provider/
│   │   ├── provider.go              # Provider interface, Result struct, shared HTTP utils
│   │   ├── github.go                # GitHub Code Search API provider
│   │   ├── getthefont.go            # getthefont.com scraper
│   │   ├── getfonts.go              # getfonts.cc scraper
│   │   └── websearch.go             # Fallback: dork queries via search API
│   ├── aggregator/
│   │   └── aggregator.go            # Fan-out/fan-in, status events, retries
│   ├── rank/
│   │   ├── normalize.go             # Normalize font filenames to canonical names
│   │   ├── tags.go                  # Parse format/weight/family from filename
│   │   └── score.go                 # Score results with configurable weights
│   ├── download/
│   │   └── download.go              # HTTP download, hash dedup, format conversion
│   ├── config/
│   │   └── config.go                # YAML config in ~/.moji/
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

Directly adapted from tork. Each source implements one contract:

```go
type Result struct {
    Name       string  // "Proxima Nova"
    Filename   string  // "ProximaNova-Bold.otf"
    Format     string  // "otf", "ttf", "woff2"
    Weight     string  // "Bold", "Regular", "Light", etc.
    SizeBytes  int64
    Source     string  // "github.com/user/repo", "getthefont.com"
    URL        string  // direct download URL
    Trusted    bool    // source flagged as reliable
}

type Provider interface {
    Name() string
    Search(ctx context.Context, query string, out chan<- Result) error
}
```

### Providers to implement

**1. GitHub Code Search (primary — covers ~80% of finds)**

```
GET /search/code?q=filename:ProximaNova+extension:ttf
GET /search/code?q=filename:ProximaNova+extension:otf
```

Requires a GitHub token. Without one, the API is severely rate-limited (60 req/hr per IP). With a token: 5,000 req/hr — effectively unlimited for normal use.

Three ways to provide it (checked in this order):

1. **`--token` flag** (per-invocation):
   ```
   moji "Proxima Nova" --token ghp_xxxxxxxxxxxx
   moji get "proxima nova bold" --token ghp_xxxxxxxxxxxx
   ```

2. **`GITHUB_TOKEN` env var** (recommended for regular use):
   ```
   export GITHUB_TOKEN=ghp_xxxxxxxxxxxx
   moji "Proxima Nova"
   ```

3. **Config file** (`~/.moji/config.yaml`):
   ```yaml
   github_token: ghp_xxxxxxxxxxxx
   ```

How to get a token: GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens → Generate new (public read access is enough, no scopes needed for public code search).

If no token is found, moji still works but falls back to unauthenticated rate limits and may hit the 60 req/hr cap during heavy use.

For each result, construct raw URL:
```
https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}
```

**2. getthefont.com** — scrapes GitHub repos that the API might miss. HTTP only (no HTTPS).

**3. getfonts.cc** — another aggregator.

**4. Web search fallback** — dork queries through a search API, parse for direct font file links:
```
site:github.com "FontName".ttf
site:vk.com "FontName".ttf
"FontName" indexof:.ttf
```

## Aggregator (fan-out/fan-in)

Lifted almost verbatim from tork. Query all providers concurrently:

```
                    ┌→ GitHub Code Search ─┐
query → aggregator ─┼→ getthefont.com    ──┼→ merged results channel → TUI
                    ├→ getfonts.cc        ─┤
                    └→ web search         ─┘
```

- Each provider runs in its own goroutine
- Results stream in as they arrive (live updating TUI)
- Per-provider status events: `searching` → `done (N results)` / `failed (err)`
- Retry with backoff on transient failures (tork pattern)
- Skip retries on `ErrBlocked` (anti-bot) or cancelled context
- `safeSearch` wraps each provider in `recover()` so one crash doesn't kill the app

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
- Extract trailing weight token (Bold, Regular, Light, Medium, Semibold, etc.)
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

## TUI (Bubble Tea)

Same screen flow as tork, with orange branding:

```
┌──────────────────────────────────────────┐
│  文字  moji                              │  ← orange logo + name
│                                          │
│  ❯ proxima nova                          │  ← search input (orange ❯)
│                                          │
│  Found 7 results:                        │
│                                          │
│  #  FORMAT  WEIGHT    SOURCE       SIZE  │  ← results list
│  1  OTF     Regular   github/...   142K  │     (sorted by score)
│  2  OTF     Bold      github/...   138K  │
│  3  TTF     Regular   getthefont   89K   │
│  4  WOFF2   Regular   github/...   31K   │
│  ...                                     │
│                                          │
│  enter: preview  D: download  /: filter  │
└──────────────────────────────────────────┘
```

Keys (tork-style):
- **home** — type to search, `↑↓` browse, `enter` open results
- **results** — `enter` preview glyphs, `D` download, `/` filter, `o` sort
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
    Query     string  // cleaned font name
    WantWeight string // "bold", "regular", "" (any)
    WantFamily bool   // "entire family" / "all weights"
    Format     string // "otf", "ttf", "" (any, prefer otf)
    Max        int    // max downloads
}
```

Parse → search → rank → select best per group → download (or `--dry-run` to preview picks).

## Config

`~/.moji/config.yaml`:

```yaml
download_dir: ~/Downloads/moji
github_token: ""                    # required for GitHub Code Search
preview_before_download: true
search_timeout_seconds: 15

ranking:
  format: 3.0
  family_size: 2.0
  trusted: 1.5
  size_penalty: 0.5
  weight_bonus: 2.0

providers:
  github:
    enabled: true
  getthefont:
    enabled: true
  getfonts:
    enabled: true
  websearch:
    enabled: false                 # fragile, opt-in
    engine: "searxng"              # or "brave"
    instance: ""
```

## Implementation Phases

### Phase 1 — MVP (working CLI)
- `provider/github.go` — GitHub Code Search API
- `aggregator` — single provider for now
- `rank` — basic format scoring
- `cmd/moji/main.go` — CLI args, print results as table
- No TUI yet, just: `moji "Proxima Nova"` → table output

### Phase 2 — TUI
- Bubble Tea search → results → download flow
- Live result streaming as providers respond
- 文字 logo, orange styling

### Phase 3 — More providers
- getthefont.com, getfonts.cc scrapers
- Web search fallback

### Phase 4 — Smart features
- `get` mode (`moji get "proxima nova bold"`)
- Family detection (find all weights from same repo)
- woff2 → ttf/otf conversion
- Hash dedup
- Glyph preview

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — styling
- [Bubbles](https://github.com/charmbracelet/bubbles) — text input, list, etc.
- [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) — config
- Standard library for HTTP, context, crypto/sha256

## Legal Note

Like tork, moji is a search client that hosts and indexes nothing. It queries public APIs and websites. Whether downloading a specific font is lawful depends on the font's license and the user's jurisdiction — the tool makes no distinction.
