import { spawn } from 'node:child_process';
import { chmod, mkdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const targets = [
  ['linux', 'x64', 'amd64'],
  ['linux', 'arm64', 'arm64'],
  ['darwin', 'x64', 'amd64'],
  ['darwin', 'arm64', 'arm64'],
  ['win32', 'x64', 'amd64'],
  ['win32', 'arm64', 'arm64'],
] as const;

const packageManifest = JSON.parse(await readFile('package.json', 'utf8')) as { version: string };
const linkFlags = [
  '-s',
  '-w',
  `-X github.com/microck/moji/internal/app.Version=${packageManifest.version}`,
  `-X github.com/microck/moji/internal/app.ReleaseMarker=moji-release-version:${packageManifest.version}:moji-marker-end`,
].join(' ');

async function build(platform: string, nodeArchitecture: string, goArchitecture: string) {
  const directory = resolve('binaries', `${platform}-${nodeArchitecture}`);
  const executable = resolve(directory, platform === 'win32' ? 'moji.exe' : 'moji');
  await mkdir(directory, { recursive: true });
  console.log(`Building ${platform}-${nodeArchitecture}...`);

  await new Promise<void>((resolveBuild, rejectBuild) => {
    const command = spawn('go', ['build', '-trimpath', `-ldflags=${linkFlags}`, '-o', executable, './cmd/moji'], {
      env: {
        ...process.env,
        CGO_ENABLED: '0',
        GOOS: platform === 'win32' ? 'windows' : platform,
        GOARCH: goArchitecture,
      },
      stdio: 'inherit',
    });
    command.once('error', rejectBuild);
    command.once('exit', (code) => {
      if (code === 0) resolveBuild();
      else rejectBuild(new Error(`go build failed for ${platform}-${nodeArchitecture} with exit code ${code}`));
    });
  });

  if (platform !== 'win32') await chmod(executable, 0o755);
}

for (const [platform, nodeArchitecture, goArchitecture] of targets) {
  await build(platform, nodeArchitecture, goArchitecture);
}
console.log(`Built ${targets.length} platform binaries.`);
