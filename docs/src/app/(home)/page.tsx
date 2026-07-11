import Link from 'next/link';

export default function HomePage() {
  return (
    <main className="mx-auto flex w-full max-w-6xl flex-1 flex-col justify-center gap-12 px-6 py-20">
      <section className="grid items-center gap-12 lg:grid-cols-[1.2fr_0.8fr]">
        <div>
          <p className="mb-4 font-mono text-sm font-semibold uppercase tracking-[0.2em] text-orange-500">文字 / moji</p>
          <h1 className="max-w-3xl text-5xl font-bold tracking-tight sm:text-7xl">Find the right font without leaving your terminal.</h1>
          <p className="mt-6 max-w-2xl text-lg text-fd-muted-foreground">
            Search multiple sources, rank complete font families, preview results in a fast TUI, and download through a safety-checked pipeline.
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <Link className="rounded-lg bg-orange-500 px-5 py-3 font-semibold text-black hover:bg-orange-400" href="/docs/tutorial">
              Get started
            </Link>
            <Link className="rounded-lg border px-5 py-3 font-semibold hover:bg-fd-accent" href="/docs/reference/cli">
              CLI reference
            </Link>
          </div>
        </div>
        <div className="rounded-2xl border bg-black p-6 font-mono text-sm text-zinc-200 shadow-2xl shadow-orange-500/10">
          <div className="mb-5 flex gap-2">
            <span className="h-3 w-3 rounded-full bg-red-400" />
            <span className="h-3 w-3 rounded-full bg-amber-400" />
            <span className="h-3 w-3 rounded-full bg-green-400" />
          </div>
          <pre aria-label="Mona, Moji's mascot" className="mb-5 text-orange-400">{`   ∧＿∧
  （ ´∀｀）  文字  moji
  （       ）
   |  |  |
  （_＿）＿）`}</pre>
          <p className="text-zinc-500">$ npm install -g @microck/moji</p>
          <p className="mt-4 text-zinc-500">$ moji get &quot;Inter bold&quot; --dry-run</p>
          <p className="mt-4 text-orange-400">Would download:</p>
          <p className="mt-2">1&nbsp;&nbsp; OTF&nbsp;&nbsp; bold&nbsp;&nbsp; getfonts.cc/...</p>
          <p className="mt-5 text-zinc-500">$ moji &quot;Source Sans&quot;</p>
          <p className="mt-2 text-orange-400">Finding results...</p>
        </div>
      </section>
      <section className="grid gap-4 md:grid-cols-3">
        {[
          ['Safe by default', 'HTTPS enforcement, redirect limits, size checks, magic-byte validation, and atomic writes.'],
          ['Human and script friendly', 'Use the live terminal interface, stable tables, or structured JSON with meaningful exit codes.'],
          ['Fast repeat searches', 'Concurrent providers, bounded retries, one-hour caching, ranking, and SHA-256 deduplication.'],
        ].map(([title, description]) => (
          <article className="rounded-xl border bg-fd-card p-6" key={title}>
            <h2 className="font-semibold">{title}</h2>
            <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">{description}</p>
          </article>
        ))}
      </section>
    </main>
  );
}
