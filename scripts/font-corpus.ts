import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { spawn } from 'node:child_process';

const fonts = [
  'Gotham',
  'Helvetica Neue',
  'Avenir',
  'Futura',
  'Brandon Grotesque',
  'Proxima Nova',
  'Univers',
  'FF DIN',
  'Knockout',
  'Garamond Premier Pro',
  'Whitney',
  'Didot',
  'Neutraface',
  'Trade Gothic',
  'Archer',
  'Akzidenz-Grotesk',
  'Frutiger',
  'Graphik',
  'Minion Pro',
  'Mrs. Eaves',
  'Belarius Serif Narrow Regular',
] as const;

const expectedMisses = new Set(['Archer', 'Belarius Serif Narrow Regular']);

type CommandResult = { code: number; stdout: string; stderr: string };
type DownloadFile = { Path: string; SHA256: string; Existing: boolean };

function run(command: string, args: readonly string[], environment: NodeJS.ProcessEnv): Promise<CommandResult> {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(command, args, { env: environment, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk: Buffer) => (stdout += chunk.toString()));
    child.stderr.on('data', (chunk: Buffer) => (stderr += chunk.toString()));
    child.once('error', rejectRun);
    child.once('exit', (code) => resolveRun({ code: code ?? 1, stdout, stderr }));
  });
}

function signature(content: Buffer): string | null {
  const magic = content.subarray(0, 4).toString('hex');
  if (magic === '4f54544f') return 'otf';
  if (magic === '00010000') return 'ttf';
  if (magic === '774f4646') return 'woff';
  if (magic === '774f4632') return 'woff2';
  return null;
}

async function invalidURLCount(cacheRoot: string): Promise<number> {
  try {
    const content = JSON.parse(await readFile(join(cacheRoot, 'moji', 'url-health.json'), 'utf8')) as {
      invalid?: Record<string, unknown>;
    };
    return Object.keys(content.invalid ?? {}).length;
  } catch {
    return 0;
  }
}

async function main(): Promise<void> {
	const argumentsList = process.argv.slice(2);
	const binaryArgument = argumentsList[0]?.startsWith('--') ? undefined : argumentsList.shift();
	const outputIndex = argumentsList.indexOf('--output-dir');
	const outputDirectory = outputIndex >= 0 ? argumentsList[outputIndex + 1] : undefined;
	if (outputIndex >= 0 && !outputDirectory) throw new Error('--output-dir requires a path');
	const unknown = argumentsList.filter((argument, index) => index !== outputIndex && index !== outputIndex + 1);
	if (unknown.length > 0) throw new Error(`unknown corpus argument: ${unknown.join(', ')}`);
  const binary = resolve(binaryArgument ?? join('binaries', `${process.platform}-${process.arch}`, process.platform === 'win32' ? 'moji.exe' : 'moji'));
  const root = await mkdtemp(join(tmpdir(), 'moji-font-corpus-'));
	try {
  const cacheRoot = join(root, 'cache');
  const environment = {
    ...process.env,
    GITHUB_TOKEN: '',
    MOJI_CONFIG: join(root, 'missing-config.yaml'),
    XDG_CACHE_HOME: cacheRoot,
  };
  const results = [];
	const kagiCheck = await run('kagi', ['--version'], environment).catch(() => ({ code: 1 }));

  for (const font of fonts) {
    const beforeInvalid = await invalidURLCount(cacheRoot);
    const destination = join(root, 'downloads', font.replaceAll(/[^a-z0-9]+/gi, '_'));
    const command = await run(binary, ['get', font, '--json', '--no-cache', '--download-dir', destination], environment);
    const afterInvalid = await invalidURLCount(cacheRoot);
    if (command.code !== 0) {
      results.push({ query: font, status: 'miss', exitCode: command.code, error: command.stderr.trim() });
      console.error(`MISS ${font}: ${command.stderr.trim()}`);
      continue;
    }

    const [download] = JSON.parse(command.stdout) as DownloadFile[];
    if (!download?.Path) throw new Error(`${font} returned success without a download path`);
    const content = await readFile(download.Path);
    const format = signature(content);
    if (format === null) throw new Error(`${font} returned bytes outside the direct-font contract`);
    const metadata = await stat(download.Path);
    results.push({
      query: font,
      status: 'pass',
      filename: basename(download.Path),
      format,
      bytes: metadata.size,
      sha256: createHash('sha256').update(content).digest('hex'),
      rejectedCandidates: Math.max(0, afterInvalid - beforeInvalid),
    });
    console.error(`PASS ${font}: ${basename(download.Path)} (${format})`);
  }

	const unexpected = results.filter((result) => result.status === 'miss' && !expectedMisses.has(result.query));
  const report = {
    generatedAt: new Date().toISOString(),
    binary,
    githubAuthenticated: false,
		kagiAvailable: kagiCheck.code === 0,
    passed: results.filter(({ status }) => status === 'pass').length,
    total: fonts.length,
    results,
  };
	if (outputDirectory) {
		await mkdir(outputDirectory, { recursive: true });
		await writeFile(join(outputDirectory, 'font-corpus.json'), `${JSON.stringify(report, null, 2)}\n`);
		const header = 'query\tstatus\tfilename\tformat\tbytes\tsha256\trejected_candidates';
		const rows = results.map((result) => {
			const clean = (value: unknown) => String(value ?? '-').replaceAll(/[\t\r\n]/g, ' ');
			return [
				result.query,
				result.status,
				'filename' in result ? result.filename : '-',
				'format' in result ? result.format : '-',
				'bytes' in result ? result.bytes : '-',
				'sha256' in result ? result.sha256 : '-',
				'rejectedCandidates' in result ? result.rejectedCandidates : 0,
			].map(clean).join('\t');
		});
		await writeFile(join(outputDirectory, 'font-corpus.tsv'), `${[header, ...rows].join('\n')}\n`);
	}
  console.log(JSON.stringify(report, null, 2));
  if (unexpected.length > 0) {
    throw new Error(`corpus changed unexpectedly for: ${unexpected.map(({ query }) => query).join(', ')}`);
  }
	} finally {
		await rm(root, { recursive: true, force: true });
	}
}

main().catch((error: unknown) => {
  console.error(error instanceof Error ? `font-corpus: ${error.message}` : error);
  process.exitCode = 1;
});
