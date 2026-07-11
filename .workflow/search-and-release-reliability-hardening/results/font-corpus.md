# 21-font direct-download corpus

Date: 2026-07-11

Command shape:

```sh
GITHUB_TOKEN= XDG_CACHE_HOME=<isolated> MOJI_CONFIG=<missing-default-config> \
  moji get "<font>" --json --no-cache --download-dir <isolated>
```

The normal default provider set was used. GitHub was tokenless and the locally
installed Kagi web-search option was available. Each font used an isolated
destination. A pass required exit code 0 and a downloaded file whose first
four bytes matched Moji's validator. No result page or gated flow counted. The
rerunnable command is `npm run corpus:verify`. Pass `--output-dir <directory>`
to regenerate JSON and TSV evidence. Exact filenames, sizes, SHA-256 hashes,
and per-query rejection counts from this run are in `font-corpus.tsv`.

| Query | Result | Validated signature |
| --- | --- | --- |
| Gotham | pass | TTF |
| Helvetica Neue | pass | OTF |
| Avenir | pass | TTF |
| Futura | pass | OTF |
| Brandon Grotesque | pass | OTF |
| Proxima Nova | pass | OTF |
| Univers | pass | WOFF2 |
| FF DIN | pass | OTF |
| Knockout | pass | OTF |
| Garamond Premier Pro | pass | WOFF2 |
| Whitney | pass | OTF |
| Didot | pass | WOFF2 |
| Neutraface | pass | OTF |
| Trade Gothic | pass | TTF |
| Archer | no direct match | - |
| Akzidenz-Grotesk | pass | TTF |
| Frutiger | pass | TTF |
| Graphik | pass | WOFF2 |
| Minion Pro | pass | OTF |
| Mrs. Eaves | pass | TTF |
| Belarius Serif Narrow Regular | no direct match | - |

Summary: 19 of 21 queries produced a byte-valid direct download. The prior
corpus produced 18 valid downloads, and Knockout now has a valid direct match.
During this run, 25 other URLs returned invalid font bytes; Moji rejected them,
recorded them in the bounded health cache, and continued to later candidates.
The invalid-first fallback itself is covered by controlled HTTP integration
tests. Archer and Belarius Serif Narrow Regular still had no direct match and
were not replaced with webpages.
