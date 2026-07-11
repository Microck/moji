<div align="center">

<img src=".github/assets/moji-logo.svg" alt="moji" width="720">

<p>
  <a href="https://www.npmjs.com/package/@microck/moji"><img src="https://img.shields.io/npm/v/@microck/moji?style=flat-square&color=000000" alt="npm version badge"></a>
  <img src="https://img.shields.io/npm/dt/@microck/moji?style=flat-square&color=000000" alt="npm total downloads badge">
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-000000?style=flat-square" alt="platform badge">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-000000?style=flat-square" alt="MIT license badge"></a>
</p>

</div>

---

ask for a font and get the file you actually meant. `moji` searches across font
sources, ranks candidates by family and filename, and downloads the best match.
browse interactively with Bubble Tea, pipe a stable table into shell workflows,
or request JSON for programmatic use.

## quick start

install with the package manager you already use:

```bash
npm install -g @microck/moji
pnpm add -g @microck/moji
bun add -g @microck/moji
```

search for a family:

```bash
moji
# or jump straight to results
moji "Inter"
```

bare `moji` opens the home TUI so you can type a query. pass a query to jump
straight to the live result list. redirect or pipe a queried command to get a
stable table instead.

```bash
moji "Inter" --format otf,ttf
moji "Inter" --format woff2 --json
```

download the best match, preview the choice first, or ask for the whole family:

```bash
moji get "Inter bold" --dry-run
moji get "Inter bold"
moji get "Inter entire family" --download-dir ~/Downloads/moji
```

## providers

the default GetFonts provider works without an account. GitHub Code Search
requires authentication, so `moji` activates it only when `GITHUB_TOKEN` or
`github_token` is configured.

```bash
export GITHUB_TOKEN=github_pat_example
moji "Inter" --provider github
```

do not pass tokens as command-line flags. use `--token-stdin` when a token only
needs to exist for one invocation.

SearXNG search is also available, but it stays disabled until both the provider
and an instance URL are configured.

## commands

| command | purpose |
| --- | --- |
| `moji` | open the home TUI and type a font query |
| `moji <query>` | search interactively or print a table when piped |
| `moji get <query>` | rank results and download the best match |
| `moji config` | create the default config when needed and open `$EDITOR` |
| `moji config show` | print the current config with its token redacted |
| `moji cache clear` | remove cached provider results |

run `moji --help` for the complete flag and example reference.

## download safety

downloads use HTTPS by default and stop at 50 MiB. before the final file appears,
`moji` validates its font magic bytes, sanitizes its filename, writes to a
temporary path, and renames it atomically. SHA-256 hashes prevent duplicate
files from being saved twice.

search results include source and best-effort license metadata. an `unknown`
license is not permission to use or redistribute a font. check the font's
license before shipping it.

## configuration

the default config lives at `~/.moji/config.yaml` and is written with mode
`0600`. set `MOJI_CONFIG` to use a different file.

```yaml
download_dir: ~/Downloads/moji
search_timeout_seconds: 15
cache_ttl_seconds: 3600
default_formats: [otf, ttf, woff2]

providers:
  github:
    enabled: true
  getfonts:
    enabled: true
  websearch:
    enabled: false
    instance: ""
```

## documentation

the Fumadocs site covers the complete workflow:

- [start here](docs/content/docs/index.mdx)
- [tutorial](docs/content/docs/tutorial.mdx)
- [CLI reference](docs/content/docs/reference/cli.mdx)
- [configuration reference](docs/content/docs/reference/configuration.mdx)
- [providers](docs/content/docs/reference/providers.mdx)
- [errors and exit codes](docs/content/docs/reference/errors-and-exit-codes.mdx)
- [architecture](docs/content/docs/explanation/architecture.mdx)

the original product and architecture research remains in
[`research/cli-design.md`](research/cli-design.md).

## development

verify the Go CLI and production documentation build together:

```bash
npm install
npm run verify
```

run the parts independently when working on one side of the repository:

```bash
make verify
npm run docs:check
npm run docs:build
npm run docs:dev
```

the end-to-end suite builds the real binary, searches a controlled HTTP
provider, downloads a valid fixture font, checks the file on disk, and exercises
cache clearing.

## license

[MIT](LICENSE)
