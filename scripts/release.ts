import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const targets = [
  { directory: 'linux-x64', executable: 'moji', goos: 'linux', goarch: 'amd64' },
  { directory: 'linux-arm64', executable: 'moji', goos: 'linux', goarch: 'arm64' },
  { directory: 'darwin-x64', executable: 'moji', goos: 'darwin', goarch: 'amd64' },
  { directory: 'darwin-arm64', executable: 'moji', goos: 'darwin', goarch: 'arm64' },
  { directory: 'win32-x64', executable: 'moji.exe', goos: 'windows', goarch: 'amd64' },
  { directory: 'win32-arm64', executable: 'moji.exe', goos: 'windows', goarch: 'arm64' },
] as const;

type BuildMetadata = { goos: string; goarch: string; revision: string | null; modified: boolean | null };
type PackageManifest = { name: string; version: string };
type TagPreflight = { tag: string; head: string; remote: 'missing' | 'matching'; localTarget: string | null };
type ReleasePreflight = 'missing-release' | 'missing-asset' | 'matching';

class CommandError extends Error {
  constructor(command: string, code: number | null, readonly stderr: string) {
    super(`${command} exited with code ${code}${stderr.trim() ? `: ${stderr.trim()}` : ''}`);
  }
}

export function isMissingReleaseError(stderr: string): boolean {
  return /(?:release not found|HTTP 404)/i.test(stderr);
}

export function validateTargets(directories: readonly string[]): void {
  const expected = targets.map(({ directory }) => directory).sort();
  const actual = [...directories].sort();
  if (actual.length !== expected.length || actual.some((directory, index) => directory !== expected[index])) {
    throw new Error(
      `release archive must contain exactly 6 native targets (${expected.join(', ')}); found ${actual.join(', ')}`,
    );
  }
}

// Go stores string constants as printable byte runs. Requiring a whole run avoids
// accepting dependency versions such as "module-v0.2.1" as the app version.
export function binaryContainsExactVersion(binary: Buffer, version: string): boolean {
  let start = 0;
  for (let index = 0; index <= binary.length; index += 1) {
    const byte = binary[index];
    const printable = byte !== undefined && byte >= 0x20 && byte <= 0x7e;
    if (printable) continue;
    if (index > start && binary.toString('ascii', start, index) === version) return true;
    start = index + 1;
  }
  return false;
}

export function parseBuildMetadata(output: string): BuildMetadata {
  const goos = output.match(/^\s*build\s+GOOS=(\S+)$/m)?.[1];
  const goarch = output.match(/^\s*build\s+GOARCH=(\S+)$/m)?.[1];
	const revision = output.match(/^\s*build\s+vcs\.revision=(\S+)$/m)?.[1] ?? null;
	const modifiedValue = output.match(/^\s*build\s+vcs\.modified=(true|false)$/m)?.[1];
  if (!goos || !goarch) throw new Error('native binary is missing GOOS/GOARCH build metadata');
	return { goos, goarch, revision, modified: modifiedValue === undefined ? null : modifiedValue === 'true' };
}

export function validateBuildSource(metadata: BuildMetadata, expectedRevision: string): void {
	if (metadata.revision !== expectedRevision || metadata.modified !== false) {
		throw new Error(
			`native binary source is ${metadata.revision ?? 'unknown'}${metadata.modified ? ' with local modifications' : ''}; expected clean commit ${expectedRevision}`,
		);
	}
}

export function validatePublishState(status: string, currentHead: string, expectedHead: string): void {
	if (status.trim()) throw new Error('release inputs changed during verification; commit them and run the release again');
	if (currentHead.trim() !== expectedHead) {
		throw new Error(`HEAD changed during verification; expected ${expectedHead}, found ${currentHead.trim()}`);
	}
}

export function archiveIntegrity(content: Buffer): string {
  return `sha512-${createHash('sha512').update(content).digest('base64')}`;
}

export function validateArtifactIntegrity(actual: string, expected: string): void {
	if (actual !== expected) throw new Error('verified release archive changed before publication');
}

async function assertArchiveUnchanged(archive: string, expectedIntegrity: string): Promise<void> {
	validateArtifactIntegrity(archiveIntegrity(await readFile(archive)), expectedIntegrity);
}

export function shouldPublishPackage(existingIntegrity: string | null, localIntegrity: string): boolean {
  if (existingIntegrity === null) return true;
  if (existingIntegrity !== localIntegrity) {
    throw new Error('the npm version already exists with different archive bytes; refusing to tag this commit');
  }
  return false;
}

export function parseRemoteAnnotatedTag(output: string, tag: string, expectedCommit: string): 'missing' | 'matching' {
	const references = new Map(
		output.trim().split('\n').filter(Boolean).map((line) => {
			const [hash, reference] = line.split(/\s+/, 2);
			return [reference, hash] as const;
		}),
	);
	const reference = `refs/tags/${tag}`;
	if (!references.has(reference)) return 'missing';
	const peeled = references.get(`${reference}^{}`);
	if (!peeled) throw new Error(`origin/${tag} exists but is not an annotated tag`);
	if (peeled !== expectedCommit) throw new Error(`origin/${tag} points to ${peeled}, not current commit ${expectedCommit}`);
	return 'matching';
}

async function run(
  command: string,
  args: readonly string[],
  options: { cwd?: string; capture?: boolean; captureStderr?: boolean } = {},
): Promise<string> {
  console.log(`> ${command} ${args.join(' ')}`);
  return await new Promise<string>((resolveRun, rejectRun) => {
    const capture = options.capture ?? false;
		const captureStderr = options.captureStderr ?? false;
    const child = spawn(command, args, {
      cwd: options.cwd,
		stdio: ['ignore', capture ? 'pipe' : 'inherit', captureStderr ? 'pipe' : 'inherit'],
    });
    let stdout = '';
		let stderr = '';
    if (capture) child.stdout?.on('data', (chunk: Buffer) => (stdout += chunk.toString()));
		if (captureStderr) child.stderr?.on('data', (chunk: Buffer) => (stderr += chunk.toString()));
    child.once('error', rejectRun);
    child.once('exit', (code) => {
      if (code === 0) resolveRun(stdout);
			else rejectRun(new CommandError(command, code, stderr));
    });
  });
}

async function readManifest(path: string): Promise<PackageManifest> {
  const manifest = JSON.parse(await readFile(path, 'utf8')) as Partial<PackageManifest>;
  if (!manifest.name || !manifest.version) throw new Error(`${path} must contain package name and version`);
  return manifest as PackageManifest;
}

async function verifyArchive(
	archive: string,
	extractionRoot: string,
	expected: PackageManifest,
	expectedRevision: string | null,
): Promise<void> {
  await run('tar', ['-xzf', archive, '-C', extractionRoot]);
  const packageRoot = join(extractionRoot, 'package');
  const packed = await readManifest(join(packageRoot, 'package.json'));
  if (packed.name !== expected.name || packed.version !== expected.version) {
    throw new Error(
      `packed manifest is ${packed.name}@${packed.version}; expected ${expected.name}@${expected.version}`,
    );
  }

  const binaryRoot = join(packageRoot, 'binaries');
  validateTargets(await readdir(binaryRoot));
  for (const target of targets) {
    const binaryPath = join(binaryRoot, target.directory, target.executable);
    const binary = await readFile(binaryPath);
    if (!binaryContainsExactVersion(binary, expected.version)) {
      throw new Error(`${target.directory} binary does not contain exact app version ${expected.version}`);
    }

    const metadata = parseBuildMetadata(await run('go', ['version', '-m', binaryPath], { capture: true }));
    if (metadata.goos !== target.goos || metadata.goarch !== target.goarch) {
      throw new Error(
        `${target.directory} contains ${metadata.goos}/${metadata.goarch}; expected ${target.goos}/${target.goarch}`,
      );
    }
		if (expectedRevision !== null) validateBuildSource(metadata, expectedRevision);
  }

  const launcherVersion = (
    await run(process.execPath, ['bin/moji.js', '--version'], { cwd: packageRoot, capture: true })
  ).trim();
  if (launcherVersion !== expected.version) {
    throw new Error(`packed JS launcher reported ${launcherVersion}; expected ${expected.version}`);
  }
}

async function assertPublishPreconditions(): Promise<string> {
  const status = await run('git', ['status', '--porcelain'], { capture: true });
  if (status.trim()) throw new Error('refusing to publish from a dirty worktree; commit the verified release first');

	await run('npm', ['whoami'], { capture: true });
	await run('gh', ['auth', 'status']);
	return (await run('git', ['rev-parse', 'HEAD'], { capture: true })).trim();
}

async function assertPublishStateUnchanged(expectedHead: string): Promise<void> {
	const status = await run('git', ['status', '--porcelain'], { capture: true });
	const currentHead = await run('git', ['rev-parse', 'HEAD'], { capture: true });
	validatePublishState(status, currentHead, expectedHead);
}

async function registryIntegrity(name: string, version: string): Promise<string | null> {
	const registry = (await run('npm', ['config', 'get', 'registry'], { capture: true })).trim().replace(/\/?$/, '/');
	const endpoint = new URL(`${encodeURIComponent(name)}/${encodeURIComponent(version)}`, registry);
	const response = await fetch(endpoint);
	if (response.status === 404) return null;
	if (!response.ok) throw new Error(`npm registry returned HTTP ${response.status} while checking ${name}@${version}`);
	const manifest = (await response.json()) as { dist?: { integrity?: unknown } };
	if (typeof manifest.dist?.integrity !== 'string' || manifest.dist.integrity === '') {
		throw new Error(`npm registry metadata for ${name}@${version} has no archive integrity`);
	}
	return manifest.dist.integrity;
}

async function inspectAnnotatedTag(version: string, expectedHead: string): Promise<TagPreflight> {
	const tag = `v${version}`;
	const head = (await run('git', ['rev-parse', 'HEAD'], { capture: true })).trim();
	validatePublishState('', head, expectedHead);
	const remoteTags = await run('git', ['ls-remote', '--tags', 'origin', `refs/tags/${tag}`, `refs/tags/${tag}^{}`], {
		capture: true,
	});
	const remote = parseRemoteAnnotatedTag(remoteTags, tag, head);
	let existingTarget: string | null = null;
	try {
		existingTarget = (await run('git', ['rev-parse', `${tag}^{}`], { capture: true })).trim();
	} catch {
		// A missing local tag is the normal first-publication path.
	}
	if (existingTarget !== null && existingTarget !== head) {
		throw new Error(`${tag} points to ${existingTarget}, not current commit ${head}`);
	}
	if (existingTarget !== null) {
		const objectType = (await run('git', ['cat-file', '-t', `refs/tags/${tag}`], { capture: true })).trim();
		if (objectType !== 'tag') throw new Error(`${tag} exists but is not an annotated tag`);
	}
	return { tag, head, remote, localTarget: existingTarget };
}

async function ensureAnnotatedTag(preflight: TagPreflight): Promise<string> {
	if (preflight.remote === 'matching') {
		await run('git', ['fetch', '--force', 'origin', `refs/tags/${preflight.tag}:refs/tags/${preflight.tag}`]);
		return preflight.tag;
	}
	if (preflight.localTarget === null) {
		await run('git', ['tag', '-a', preflight.tag, preflight.head, '-m', `Moji ${preflight.tag}`]);
	}
	await run('git', ['push', 'origin', preflight.tag]);
	return preflight.tag;
}

async function inspectGitHubRelease(
	tag: string,
	archive: string,
	temporaryRoot: string,
	expectedIntegrity: string,
): Promise<ReleasePreflight> {
	let release: { assets?: Array<{ name?: string }> } | null = null;
	try {
		release = JSON.parse(await run('gh', ['release', 'view', tag, '--json', 'assets'], { capture: true, captureStderr: true })) as {
			assets?: Array<{ name?: string }>;
		};
	} catch (error: unknown) {
		if (error instanceof CommandError && isMissingReleaseError(error.stderr)) return 'missing-release';
		throw error;
	}
	const archiveName = basename(archive);
	if (!release.assets?.some(({ name }) => name === archiveName)) return 'missing-asset';
	const downloadRoot = join(temporaryRoot, 'github-release-asset');
	await rm(downloadRoot, { recursive: true, force: true });
	await mkdir(downloadRoot);
	await run('gh', ['release', 'download', tag, '--pattern', archiveName, '--dir', downloadRoot]);
	const existingIntegrity = archiveIntegrity(await readFile(join(downloadRoot, archiveName)));
	if (existingIntegrity !== expectedIntegrity) {
		throw new Error(`GitHub release ${tag} contains ${archiveName} with different archive bytes`);
	}
	return 'matching';
}

async function ensureGitHubRelease(
	preflight: ReleasePreflight,
	tag: string,
	archive: string,
	expectedIntegrity: string,
): Promise<void> {
	await assertArchiveUnchanged(archive, expectedIntegrity);
	if (preflight === 'missing-release') {
		await run('gh', [
			'release',
			'create',
			tag,
			archive,
			'--verify-tag',
			'--latest',
			'--generate-notes',
			'--title',
			`Moji ${tag}`,
		]);
		return;
	}
	if (preflight === 'missing-asset') {
		await run('gh', ['release', 'upload', tag, archive]);
		return;
	}
	console.log(`GitHub release ${tag} already contains the verified archive; publication is complete.`);
}

async function main(): Promise<void> {
  const publish = process.argv.slice(2).includes('--publish');
  const unknown = process.argv.slice(2).filter((argument) => argument !== '--publish' && argument !== '--verify');
  if (unknown.length > 0) throw new Error(`unknown release argument: ${unknown.join(', ')}`);

  const projectRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
  process.chdir(projectRoot);
  const manifest = await readManifest(join(projectRoot, 'package.json'));
	const publishHead = publish ? await assertPublishPreconditions() : null;

  const temporaryRoot = await mkdtemp(join(tmpdir(), 'moji-release-'));
  try {
    // Build explicitly, then force lifecycle scripts during packing. The second
    // build through prepack is intentional protection against ignore-scripts=true.
    await run('npm', ['run', 'build:binaries']);
    const packRoot = join(temporaryRoot, 'pack');
    await mkdir(packRoot);
    await run('npm', ['pack', '--ignore-scripts=false', '--pack-destination', packRoot]);
    const archives = (await readdir(packRoot)).filter((entry) => entry.endsWith('.tgz'));
    if (archives.length !== 1) throw new Error(`npm pack produced ${archives.length} archives; expected exactly one`);
    const archive = join(packRoot, archives[0]);
    const extractionRoot = join(temporaryRoot, 'extract');
    await mkdir(extractionRoot);
    await verifyArchive(archive, extractionRoot, manifest, publishHead);
    console.log(`Verified ${basename(archive)} with all 6 native binaries at version ${manifest.version}.`);

    if (!publish) {
      console.log('Verification complete. No package, tag, or GitHub release was published.');
      return;
    }
		await assertPublishStateUnchanged(publishHead);

		const localIntegrity = archiveIntegrity(await readFile(archive));
		const existingIntegrity = await registryIntegrity(manifest.name, manifest.version);
		const tagPreflight = await inspectAnnotatedTag(manifest.version, publishHead);
		const releasePreflight = await inspectGitHubRelease(tagPreflight.tag, archive, temporaryRoot, localIntegrity);
		await assertPublishStateUnchanged(publishHead);
		if (shouldPublishPackage(existingIntegrity, localIntegrity)) {
			await assertArchiveUnchanged(archive, localIntegrity);
			await run('npm', ['publish', archive, '--access', 'public', '--ignore-scripts=false']);
		} else {
			console.log(`${manifest.name}@${manifest.version} already contains the verified archive; resuming publication.`);
		}
		validateArtifactIntegrity((await registryIntegrity(manifest.name, manifest.version)) ?? '', localIntegrity);
		const tag = await ensureAnnotatedTag(tagPreflight);
		await ensureGitHubRelease(releasePreflight, tag, archive, localIntegrity);
		if ((await inspectGitHubRelease(tag, archive, temporaryRoot, localIntegrity)) !== 'matching') {
			throw new Error(`GitHub release ${tag} did not retain the verified archive`);
		}
    console.log(`Published ${manifest.name}@${manifest.version} and GitHub release ${tag}.`);
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error: unknown) => {
    console.error(error instanceof Error ? `release: ${error.message}` : error);
    process.exitCode = 1;
  });
}
