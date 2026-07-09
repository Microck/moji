# moji

A cozy terminal tool to find and download fonts by name. Modeled after [tork](https://github.com/melqtx/tork) — same architecture (provider → aggregator → rank → TUI), adapted for font search instead of torrents.

```
 文字

 moji — find fonts from the terminal
```

**Language:** Go (Bubble Tea TUI)

**How it works:** Query public sources (GitHub Code Search API, font aggregators) concurrently, rank results by format/weight/family completeness, pick and download.

**Status:** Design phase. See the [CLI design doc](research/cli-design.md) for full architecture.

## Architecture (summary)

```
                    ┌→ GitHub Code Search ─┐
query → aggregator ─┼→ getthefont.com    ──┼→ rank → TUI results → download
                    ├→ getfonts.cc        ─┤
                    └→ web search         ─┘
```

Each source is a Provider implementing one interface. The aggregator fans out concurrently, streams results back live. Rank normalizes filenames and scores by format preference, family size, source trust, and weight match.

## Quick Start

Not yet implemented. Planned:
```
moji "Proxima Nova"                          # interactive TUI
moji "Proxima Nova" --download               # auto-download best match
moji get "proxima nova bold" --dry-run       # plan only, no interaction
```
